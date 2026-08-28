package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ConfigRef is the ref holding the repo-global meads configuration, stored
// the same way as a task ref (see gitstore.go): commit -> tree -> a single
// JSON blob, ConfigFileName.
const ConfigRef = "refs/meads/config"

// ConfigFileName is the path of the config JSON blob within ConfigRef's tree.
const ConfigFileName = "config.json"

// GitRefProtocolVersion is the newest refs/meads/* storage protocol this
// package understands. It versions semantics, not the shape of Config: bump
// it whenever an older md could read the same refs but interpret or mutate
// them incorrectly.
//
// The value is written to config.json as "git_ref_protocol_version" before
// every git-mode mutation. Repositories created before this field existed are
// the original protocol and therefore read as version 1. Keeping that legacy
// value separate from this constant is deliberate: when this constant is
// eventually bumped, a missing field must continue to mean 1 rather than
// silently becoming the new version.
const GitRefProtocolVersion = 1

const (
	legacyGitRefProtocolVersion = 1
	gitRefProtocolVersionKey    = "git_ref_protocol_version"
)

// ErrGitRefProtocolUpgradeRequired is returned when refs/meads/* advertises
// a protocol newer than this md understands. Callers can match it with
// errors.Is while the rendered error also names both versions and tells the
// user what to do.
var ErrGitRefProtocolUpgradeRequired = errors.New("git-ref protocol requires a newer md")

// defaultPushInterval is the PushInterval applied when the field is unset or
// unparsable. Always a valid input to time.ParseDuration - see
// PushIntervalDuration and DefaultConfig.
const defaultPushInterval = "1m"

// Config is the repo-global meads configuration, stored at ConfigRef so
// every clone and every agent shares one value rather than each guessing its
// own.
//
// RemoteLocking deliberately has no per-clone override: it must NOT be read
// from local git config, an environment variable, or a local file. A lock
// protocol only works if every participant agrees to honor it; a per-clone
// setting would let one misconfigured agent silently void mutual exclusion
// for everyone else, with no error surfacing anywhere. It lives only in this
// shared ref.
type Config struct {
	RemoteLocking bool `json:"remote_locking,omitempty"`
	// PushInterval is the CLI background-sync debounce delay. The JSON name is
	// retained for compatibility with repositories created by older releases.
	PushInterval string `json:"push_interval,omitempty"` // a Go duration string
}

// knownConfigKeys are the config.json keys this storage layer owns: Config's
// fields plus the protocol marker. SetConfig preserves every other key, so a
// field a newer meads version adds survives an older binary's rewrite.
var knownConfigKeys = map[string]bool{
	gitRefProtocolVersionKey: true,
	"remote_locking":         true,
	"push_interval":          true,
}

// DefaultConfig returns the defaults applied when the config ref is absent,
// or when a field within an existing config is unset.
func DefaultConfig() Config {
	return Config{
		RemoteLocking: false,
		PushInterval:  defaultPushInterval,
	}
}

// applyConfigDefaults fills any unset field of cfg with its default.
// RemoteLocking's zero value (false) already IS its default, so only
// PushInterval ever needs filling.
func applyConfigDefaults(cfg Config) Config {
	if cfg.PushInterval == "" {
		cfg.PushInterval = defaultPushInterval
	}
	return cfg
}

// PushIntervalDuration parses PushInterval, falling back to the default
// (defaultPushInterval, always valid) on empty or malformed input.
func (c Config) PushIntervalDuration() time.Duration {
	s := c.PushInterval
	if s == "" {
		s = defaultPushInterval
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		d, _ = time.ParseDuration(defaultPushInterval)
	}
	return d
}

// Config returns the repo's current configuration, applying defaults for an
// absent ref or for any unset field. An absent ref is not an error.
//
// The cache is keyed by ConfigRef's resolved oid: ResolveRef is one cheap
// for-each-ref call, so it always runs - detecting a changed, newly-created,
// or newly-deleted ref - but the blob is only read and re-parsed when the
// resolved oid differs from the oid the cache was last populated from. In
// particular an absent ref is cached as "populated at ZeroOID", never as a
// bare flag that would keep returning defaults forever even after a config
// ref is later created (see TestGitConfig_AbsentThenCreated_NewValueIsSeen).
func (g *GitStore) Config() (Config, error) {
	cfg, _, _, err := g.configSnapshot()
	return cfg, err
}

// configSnapshot returns the user configuration plus the git-ref protocol
// version stored beside it. explicit is false only for a legacy config (or no
// config ref at all) that predates git_ref_protocol_version. The same
// oid-keyed cache as Config avoids re-reading config.json on every task
// operation while ResolveRef still notices an external config change.
func (g *GitStore) configSnapshot() (cfg Config, protocolVersion int, explicit bool, err error) {
	oid, err := g.refs.ResolveRef(ConfigRef)
	if err != nil {
		if !errors.Is(err, ErrRefNotFound) {
			return Config{}, 0, false, err
		}
		oid = ZeroOID
	}

	if cfg, protocolVersion, explicit, ok := g.cachedConfig(oid); ok {
		return cfg, protocolVersion, explicit, nil
	}

	if oid == ZeroOID {
		cfg := DefaultConfig()
		g.storeConfigCache(ZeroOID, cfg, legacyGitRefProtocolVersion, false)
		return cfg, legacyGitRefProtocolVersion, false, nil
	}

	content, readOID, err := g.refs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			// Deleted between the ResolveRef above and this read: treat like
			// any other absent ref rather than surfacing a transient error.
			cfg := DefaultConfig()
			g.storeConfigCache(ZeroOID, cfg, legacyGitRefProtocolVersion, false)
			return cfg, legacyGitRefProtocolVersion, false, nil
		}
		return Config{}, 0, false, err
	}
	protocolVersion, explicit, err = gitRefProtocolVersionFromJSON(content, ConfigRef)
	if err != nil {
		return Config{}, 0, false, err
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, 0, false, fmt.Errorf("parsing %s at %s: %w", ConfigFileName, ConfigRef, err)
	}
	cfg = applyConfigDefaults(cfg)
	g.storeConfigCache(readOID, cfg, protocolVersion, explicit)
	return cfg, protocolVersion, explicit, nil
}

// gitRefProtocolVersionFromJSON reads and validates the protocol marker from
// one config.json. Missing means the pre-marker v1 protocol. A newer version
// must fail before this binary reads or writes task refs: the JSON may still
// parse while its meaning no longer does.
func gitRefProtocolVersionFromJSON(content []byte, ref string) (version int, explicit bool, err error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return 0, false, fmt.Errorf("parsing %s at %s: %w", ConfigFileName, ref, err)
	}
	return gitRefProtocolVersionFromRaw(raw, ref)
}

func gitRefProtocolVersionFromRaw(raw map[string]json.RawMessage, ref string) (version int, explicit bool, err error) {
	encoded, ok := raw[gitRefProtocolVersionKey]
	if !ok {
		return legacyGitRefProtocolVersion, false, nil
	}
	if err := json.Unmarshal(encoded, &version); err != nil || version < 1 {
		if err == nil {
			err = fmt.Errorf("must be a positive integer")
		}
		return 0, true, fmt.Errorf("parsing %s at %s: invalid %s: %w", ConfigFileName, ref, gitRefProtocolVersionKey, err)
	}
	if version > GitRefProtocolVersion {
		return 0, true, fmt.Errorf("%w: repository uses version %d, but this md supports up to version %d; upgrade md",
			ErrGitRefProtocolUpgradeRequired, version, GitRefProtocolVersion)
	}
	return version, true, nil
}

// cachedConfig returns the cached config and true if the cache was populated
// against exactly oid. g.configOID's zero value ("") means the cache has
// never been populated, which is distinct from ZeroOID (a confirmed-absent
// ref) - so a fresh GitStore always misses on its first call regardless of
// whether oid itself is ZeroOID.
func (g *GitStore) cachedConfig(oid OID) (Config, int, bool, bool) {
	g.configMu.RLock()
	defer g.configMu.RUnlock()
	if g.configOID == "" || g.configOID != oid {
		return Config{}, 0, false, false
	}
	return g.configCache, g.configProtocolVersion, g.configProtocolExplicit, true
}

// storeConfigCache records cfg as the parsed-and-defaulted value for
// ConfigRef as of oid.
func (g *GitStore) storeConfigCache(oid OID, cfg Config, protocolVersion int, protocolExplicit bool) {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.configOID = oid
	g.configCache = cfg
	g.configProtocolVersion = protocolVersion
	g.configProtocolExplicit = protocolExplicit
}

// EnsureGitRefProtocolVersion makes the current protocol marker durable.
// It is called before every refs/meads/* mutation, so changing protocol
// semantics and bumping GitRefProtocolVersion makes the first write by that
// md version publish the compatibility boundary before publishing data that
// relies on it. It returns true only when it wrote ConfigRef.
//
// Today the only implicit upgrade is a missing marker -> version 1, because
// missing is exactly the format version 1 describes. A future protocol bump
// must add its real data migration here rather than merely relabeling older
// refs with new semantics.
func (g *GitStore) EnsureGitRefProtocolVersion() (bool, error) {
	_, version, explicit, err := g.configSnapshot()
	if err != nil {
		return false, err
	}
	if explicit && version == GitRefProtocolVersion {
		return false, nil
	}
	if explicit && version != GitRefProtocolVersion {
		return false, fmt.Errorf("git-ref protocol version %d must be migrated to version %d before writing refs/meads/*", version, GitRefProtocolVersion)
	}

	// Re-read and CAS the raw object rather than passing the snapshot's Config
	// through SetConfig. Another writer may change a setting between the two;
	// replaying our stale defaults would erase that winner while all we mean to
	// add is one metadata key.
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		raw, oid, err := g.readConfigRaw()
		if err != nil {
			return false, err
		}
		version := GitRefProtocolVersion // a brand-new config starts at current
		explicit := false
		if raw != nil {
			version, explicit, err = gitRefProtocolVersionFromRaw(raw, ConfigRef)
			if err != nil {
				return false, err
			}
		}
		if explicit && version == GitRefProtocolVersion {
			return false, nil // another writer stamped it after our snapshot
		}
		if version != GitRefProtocolVersion {
			return false, fmt.Errorf("git-ref protocol version %d must be migrated to version %d before writing refs/meads/*", version, GitRefProtocolVersion)
		}
		if raw == nil {
			raw = make(map[string]json.RawMessage)
		}
		raw[gitRefProtocolVersionKey] = json.RawMessage(strconv.Itoa(version))
		merged, err := json.Marshal(raw)
		if err != nil {
			return false, fmt.Errorf("marshaling config with git-ref protocol version: %w", err)
		}
		newOID, err := g.refs.CommitFile(ConfigRef, ConfigFileName, merged, oid, "set git-ref protocol version")
		if err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return false, fmt.Errorf("writing git-ref protocol version: %w", err)
			}
			lastErr = err
			continue
		}
		var cfg Config
		if err := json.Unmarshal(merged, &cfg); err != nil {
			return false, fmt.Errorf("re-parsing config with git-ref protocol version: %w", err)
		}
		g.storeConfigCache(newOID, applyConfigDefaults(cfg), version, true)
		return true, nil
	}
	return false, fmt.Errorf("writing git-ref protocol version: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// CheckGitRefProtocol validates the local protocol marker without writing it.
// Ordinary task methods call it internally; long-running consumers can use it
// to fail at startup rather than on their first request.
func (g *GitStore) CheckGitRefProtocol() error {
	_, _, _, err := g.configSnapshot()
	return err
}

// SetConfig writes cfg to ConfigRef via compare-and-swap - never an
// unconditional write - so concurrent writers cannot clobber each other.
// Bounded by maxCASRetries exactly like GitStore's task mutations
// (gitmutate.go): every attempt re-reads ConfigRef's current stored JSON and
// re-derives the merged result from scratch, never replaying a decision made
// against a now-stale read. If the ref is absent, the first attempt's read
// reports ZeroOID and CommitFile performs a create-only CAS.
//
// Any config.json object key Config's fields don't know about is preserved
// verbatim (see mergeConfig): a newer meads version can add a field without
// an older binary's SetConfig calls erasing it on the next read/modify/write.
func (g *GitStore) SetConfig(cfg Config) error {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		raw, oid, err := g.readConfigRaw()
		if err != nil {
			return err
		}
		protocolVersion := GitRefProtocolVersion
		if raw != nil {
			protocolVersion, _, err = gitRefProtocolVersionFromRaw(raw, ConfigRef)
			if err != nil {
				return err
			}
		}
		merged, err := mergeConfig(raw, cfg, protocolVersion)
		if err != nil {
			return err
		}
		newOID, err := g.refs.CommitFile(ConfigRef, ConfigFileName, merged, oid, "update config")
		if err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		g.storeConfigCache(newOID, applyConfigDefaults(cfg), protocolVersion, true)
		return nil
	}
	return fmt.Errorf("set config: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// readConfigRaw reads ConfigRef's current config.json as a raw JSON object
// (preserving unknown keys, see mergeConfig) along with the ref's current
// oid. Returns a nil map and ZeroOID if the ref does not exist yet - a
// legitimate starting point for SetConfig's create-only CAS, not an error.
func (g *GitStore) readConfigRaw() (map[string]json.RawMessage, OID, error) {
	content, oid, err := g.refs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return nil, ZeroOID, nil
		}
		return nil, "", err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, "", fmt.Errorf("parsing %s at %s: %w", ConfigFileName, ConfigRef, err)
	}
	if _, _, err := gitRefProtocolVersionFromRaw(raw, ConfigRef); err != nil {
		return nil, "", err
	}
	return raw, oid, nil
}

// mergeConfig overlays cfg's fields and the storage-owned protocol marker
// onto raw, returning bytes ready to commit. Config fields are fully replaced,
// including being dropped when their value is zero; unknown keys pass through
// untouched.
func mergeConfig(raw map[string]json.RawMessage, cfg Config, protocolVersion int) ([]byte, error) {
	knownJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(knownJSON, &known); err != nil {
		return nil, fmt.Errorf("re-parsing marshaled config: %w", err)
	}

	merged := make(map[string]json.RawMessage, len(raw)+len(known))
	for k, v := range raw {
		merged[k] = v
	}
	for k := range knownConfigKeys {
		delete(merged, k) // cfg is authoritative for known fields, even when absent
	}
	for k, v := range known {
		merged[k] = v
	}
	// Protocol metadata is owned by the storage layer, not by Config callers.
	// A new config starts at this binary's version; an existing config keeps
	// its validated version until EnsureGitRefProtocolVersion performs the
	// corresponding protocol migration.
	merged[gitRefProtocolVersionKey] = json.RawMessage(strconv.Itoa(protocolVersion))

	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	return out, nil
}
