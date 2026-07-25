#!/usr/bin/env bash
# Probe a git host for the four properties meads' ref backend needs.
# See doc/GIT_REF_TESTS.md for what each property means and why.
#
#   usage: ./doc/git-ref-probe.sh <https-remote-url>
#
# Creates refs under refs/meads/probe/* and deletes them on exit.
# Property 4 (server-side CAS) speaks smart-HTTP directly, so it needs an
# HTTPS URL plus credentials git already has (~/.netrc or a credential helper).
set -uo pipefail

REMOTE="${1:-}"
if [ -z "$REMOTE" ]; then
  echo "usage: $0 <https-remote-url>" >&2
  exit 2
fi

TARGET=refs/meads/probe/target
SCRATCH=refs/meads/probe/scratch
PASS=0; FAIL=0

ok()   { echo "  PASS  $*"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL  $*"; FAIL=$((FAIL+1)); }
head_() { echo; echo "$*"; }

WORK=$(mktemp -d)
cleanup() {
  if [ -d "$WORK/repo" ]; then
    git -C "$WORK/repo" push -q origin --delete "$TARGET" "$SCRATCH" 2>/dev/null
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "probing $REMOTE"
git clone -q "$REMOTE" "$WORK/repo" 2>/dev/null || git init -q "$WORK/repo"
cd "$WORK/repo" || exit 1
git remote add origin "$REMOTE" 2>/dev/null
git remote set-url origin "$REMOTE"

blob() { printf '%s' "$1" | git hash-object -w --stdin; }
A=$(blob '{"probe":"A"}'); B=$(blob '{"probe":"B"}'); C=$(blob '{"probe":"C"}')

# --- 1 + 2: custom namespace, ref pointing straight at a blob -----------------
head_ "1+2. custom namespace + blob-valued ref"
git update-ref "$TARGET" "$A"
if git push -q origin "$TARGET:$TARGET" 2>/dev/null; then
  ok "pushed $TARGET -> blob"
else
  bad "host refused push to $TARGET (namespace restricted?)"
  echo; echo "result: $PASS passed, $FAIL failed"; exit 1
fi

# --- 3: advertised + fetchable into a fresh clone -----------------------------
head_ "3. advertised and fetchable"
if [ -n "$(git ls-remote origin "$TARGET" 2>/dev/null)" ]; then
  ok "advertised in ls-remote"
else
  bad "not advertised in ls-remote (hidden ref?)"
fi

git clone -q "$REMOTE" "$WORK/fresh" 2>/dev/null || git init -q "$WORK/fresh"
git -C "$WORK/fresh" remote add origin "$REMOTE" 2>/dev/null
if git -C "$WORK/fresh" fetch -q origin "refs/meads/*:refs/meads/*" 2>/dev/null &&
   [ "$(git -C "$WORK/fresh" cat-file -p "$TARGET" 2>/dev/null)" = '{"probe":"A"}' ] &&
   [ "$(git -C "$WORK/fresh" cat-file -t "$TARGET" 2>/dev/null)" = blob ]; then
  ok "round-tripped into a fresh clone, target type is blob"
else
  bad "could not fetch/read the ref back from a fresh clone"
fi

# --- 4: server-enforced compare-and-swap --------------------------------------
head_ "4. server-enforced compare-and-swap"
git update-ref "$SCRATCH" "$C"
git push -q origin "$SCRATCH:$SCRATCH" 2>/dev/null   # make C exist server-side
git update-ref "$TARGET" "$B"
git push -q origin --force "$TARGET:$TARGET" 2>/dev/null  # remote target is now B

cas() { # cas <claimed-old-oid>  -> prints "ok" / "ng" / "err:…"
  python3 - "$REMOTE" "$1" "$C" "$TARGET" <<'PY'
import sys, base64, subprocess, urllib.request, urllib.error, urllib.parse
remote, old, new, ref = sys.argv[1:5]
u = urllib.parse.urlparse(remote)
if u.scheme != "https":
    print("err:needs-https"); sys.exit()

import os
user = os.environ.get("GIT_USERNAME") or "x-access-token"
pw = os.environ.get("GIT_PASSWORD") or os.environ.get("GITHUB_TOKEN")
try:  # else prefer whatever git itself would use (covers credential helpers)
    if pw: raise StopIteration
    q = f"protocol=https\nhost={u.hostname}\npath={u.path.lstrip('/')}\n\n"
    out = subprocess.run(["git","credential","fill"], input=q, capture_output=True,
                         text=True, timeout=20).stdout
    kv = dict(l.split("=",1) for l in out.splitlines() if "=" in l)
    user, pw = kv.get("username") or user, kv.get("password")
except StopIteration:
    pass
except Exception:
    pass
if not pw:
    try:
        import netrc
        user, _, pw = netrc.netrc().authenticators(u.hostname)
    except Exception:
        print("err:no-credentials"); sys.exit()

def pkt(s): return ("%04x" % (len(s)+4)).encode() + s
body  = pkt(b"%s %s %s\x00report-status" % (old.encode(), new.encode(), ref.encode()) + b"\n")
body += b"0000"
body += bytes.fromhex("5041434b0000000200000000029d08823bd8a8eab510ad6ac75c823cfd3ed31e")

url = remote if remote.endswith(".git") else remote + ".git"
req = urllib.request.Request(url + "/git-receive-pack", data=body, method="POST",
    headers={"Content-Type":"application/x-git-receive-pack-request",
             "Accept":"application/x-git-receive-pack-result",
             "Authorization":"Basic "+base64.b64encode(f"{user}:{pw}".encode()).decode()})
try:
    r = urllib.request.urlopen(req, timeout=30).read().decode("utf-8","replace")
except urllib.error.HTTPError as e:
    print(f"err:http-{e.code}"); sys.exit()
except Exception as e:
    print(f"err:{type(e).__name__}"); sys.exit()
print("ng" if f"ng {ref}" in r else "ok" if f"ok {ref}" in r else "err:unparsed")
PY
}

FALSE_RESULT=$(cas "$A")   # claim A when the ref is really at B
case "$FALSE_RESULT" in
  ng) if [ "$(git ls-remote origin "$TARGET" | cut -f1)" = "$B" ]; then
        ok "false old-oid rejected, ref unchanged"
      else
        bad "false old-oid rejected but ref moved anyway"
      fi ;;
  ok) bad "SERVER ACCEPTED A FALSE OLD-OID — no server-side CAS" ;;
  *)  echo "  SKIP  CAS probe inconclusive ($FALSE_RESULT)" ;;
esac

if [ "$FALSE_RESULT" = ng ] || [ "$FALSE_RESULT" = ok ]; then
  # positive control: identical request, correct old-oid, must succeed
  if [ "$(cas "$B")" = ok ] && [ "$(git ls-remote origin "$TARGET" | cut -f1)" = "$C" ]; then
    ok "positive control: true old-oid accepted, ref moved"
  else
    bad "positive control failed — rejection above may not be CAS"
  fi
fi

echo; echo "result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
