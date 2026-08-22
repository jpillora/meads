package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConfigRef is the ref holding the repo-global meads configuration, stored
// the same way as a task ref (see gitstore.go): commit -> tree -> a single
// JSON blob, ConfigFileName.
const ConfigRef = "refs/meads/config"

// ConfigFileName is the path of the config JSON blob within ConfigRef's tree.
const ConfigFileName = "config.json"

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

// knownConfigKeys are the config.json object keys Config's fields own.
// SetConfig preserves every other key found in the stored JSON untouched, so
// a field a newer meads version adds survives being read and re-written by
// an older binary that doesn't know about it (see mergeConfig).
var knownConfigKeys = map[string]bool{
	"remote_locking": true,
	"push_interval":  true,
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
	oid, err := g.refs.ResolveRef(ConfigRef)
	if err != nil {
		if !errors.Is(err, ErrRefNotFound) {
			return Config{}, err
		}
		oid = ZeroOID
	}

	if cfg, ok := g.cachedConfig(oid); ok {
		return cfg, nil
	}

	if oid == ZeroOID {
		cfg := DefaultConfig()
		g.storeConfigCache(ZeroOID, cfg)
		return cfg, nil
	}

	content, readOID, err := g.refs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			// Deleted between the ResolveRef above and this read: treat like
			// any other absent ref rather than surfacing a transient error.
			cfg := DefaultConfig()
			g.storeConfigCache(ZeroOID, cfg)
			return cfg, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s at %s: %w", ConfigFileName, ConfigRef, err)
	}
	cfg = applyConfigDefaults(cfg)
	g.storeConfigCache(readOID, cfg)
	return cfg, nil
}

// cachedConfig returns the cached config and true if the cache was populated
// against exactly oid. g.configOID's zero value ("") means the cache has
// never been populated, which is distinct from ZeroOID (a confirmed-absent
// ref) - so a fresh GitStore always misses on its first call regardless of
// whether oid itself is ZeroOID.
func (g *GitStore) cachedConfig(oid OID) (Config, bool) {
	g.configMu.RLock()
	defer g.configMu.RUnlock()
	if g.configOID == "" || g.configOID != oid {
		return Config{}, false
	}
	return g.configCache, true
}

// storeConfigCache records cfg as the parsed-and-defaulted value for
// ConfigRef as of oid.
func (g *GitStore) storeConfigCache(oid OID, cfg Config) {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.configOID = oid
	g.configCache = cfg
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
		merged, err := mergeConfig(raw, cfg)
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
		g.storeConfigCache(newOID, applyConfigDefaults(cfg))
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
	return raw, oid, nil
}

// mergeConfig overlays cfg's known fields onto raw - a JSON object that may
// carry keys Config's fields don't know about - and returns the merged
// object as bytes ready to commit. Known keys (knownConfigKeys) are always
// fully replaced by cfg, including being dropped entirely when cfg's value
// is the zero value (matching Config's omitempty tags, and Config()'s
// fallback to defaults for an absent key); every other key in raw passes
// through untouched, which is the forward-compatibility guarantee.
func mergeConfig(raw map[string]json.RawMessage, cfg Config) ([]byte, error) {
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

	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	return out, nil
}
