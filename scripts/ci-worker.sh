#!/bin/sh
# The worker machine the release's full-suite job adds to itself: one Compute
# Engine instance per run, created before `dispat run tests --since all`,
# serving that sweep alone, and deleted when the job ends whatever the suite
# did. The job is the orchestrator; this script only provisions the second
# node and starts `dispat worker` on it, so the release logic stays dispat's
# and the workflow stays plumbing.
#
#   sh scripts/ci-worker.sh create          create the instance and wait until it answers
#   sh scripts/ci-worker.sh start <dispat>  install that binary and start the worker on it
#   sh scripts/ci-worker.sh prune           delete this run's coordination branches left on it
#   sh scripts/ci-worker.sh collect <dir>   copy the worker's log into <dir>
#   sh scripts/ci-worker.sh delete          delete the instance (a missing one is not an error)
#
# The mailbox is the repository being released. The orchestrator names the
# node alone (`--worker "$DISPAT_CI_WORKER_NODE"`) and reaches the repository
# with the credential its checkout persisted; the worker polls the same
# repository over https with a token of its own, DISPAT_CI_WORKER_TOKEN, which
# the job mints for the run from a GitHub App, never the job's GITHUB_TOKEN.
#
# Everything the steps share lives in DISPAT_CI_WORKER_DIR: the ephemeral ssh
# key pair, the instance's address and its host keys. The signing secret
# (DISPAT_EXECUTION_SECRET) and the token are in the environment of `start`;
# both travel to the machine over ssh into files only the worker's user reads,
# and neither is ever written into instance metadata, which anyone with
# project access can read.
#
# gcloud runs on the host when it is installed, and otherwise inside the
# release tools image exactly as the infra and docs stages run it, with the
# Workload Identity credential files the auth step wrote reachable at their
# own paths. The instance carries no service account: it needs no cloud
# credential to serve tasks, and a task that ran on it could not acquire one.
set -eu

command=${1:-}
[ -n "$command" ] || { echo "usage: ci-worker.sh create|start|prune|collect|delete" >&2; exit 2; }
shift

dir=${DISPAT_CI_WORKER_DIR:-${RUNNER_TEMP:-/tmp}/dispat-ci-worker}
run_id=${GITHUB_RUN_ID:-local}
attempt=${GITHUB_RUN_ATTEMPT:-1}
name=${DISPAT_CI_WORKER_NAME:-dispat-ci-worker-$run_id-$attempt}
# The zones tried in turn: a zone can be out of a machine type for a while,
# and a run waits for nobody. The zone the instance landed in is remembered
# for every later step.
zones=${DISPAT_CI_WORKER_ZONES:-us-central1-a,us-central1-b,us-central1-c,us-central1-f}
# The machine types tried in turn, the first being the one the release is
# measured on; a zone can be out of one of them for a while, so every zone is
# asked for the first type before any is asked for the second.
machines=${DISPAT_CI_WORKER_MACHINES:-c3-standard-8,n2-standard-8,e2-standard-8}
disk_gb=${DISPAT_CI_WORKER_DISK_GB:-100}
# The instance deletes itself at this age even if the job that made it died
# before its delete step; a suite that legitimately runs longer raises it.
lifetime=${DISPAT_CI_WORKER_LIFETIME:-4h}
# The node's name: the orchestrator's link and the worker's own
# execution.name. A run names it after itself, so the coordination branches of
# one run are told apart from every other run's by name alone.
node=${DISPAT_CI_WORKER_NODE:-ci-worker}
user=dispat
ready_marker=/var/lib/dispat-worker-ready
failed_marker=/var/lib/dispat-worker-failed

log() { printf 'ci-worker: %s\n' "$*" >&2; }
fail() { log "$*"; exit 1; }

zone_of_instance() {
  cat "$dir/zone" 2>/dev/null || printf '%s' "${zones%%,*}"
}

run_gcloud() {
  if command -v gcloud >/dev/null 2>&1; then
    gcloud "$@"
    return
  fi
  root=$(git rev-parse --show-toplevel)
  if ! docker image inspect dispat-release-tools >/dev/null 2>&1; then
    dispat exec tools-image >&2
  fi
  gcloud_config=""
  [ -n "${GOOGLE_GHA_CREDS_PATH:-}" ] || [ ! -d "$HOME/.config/gcloud" ] \
    || gcloud_config="-v $HOME/.config/gcloud:/root/.config/gcloud"
  # shellcheck disable=SC2086
  docker run --rm -v "$root:$root" -v "$dir:$dir" -w "$root" $gcloud_config \
    -e GOOGLE_APPLICATION_CREDENTIALS -e GOOGLE_GHA_CREDS_PATH \
    -e CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE -e CLOUDSDK_CORE_PROJECT \
    -e GOOGLE_CLOUD_PROJECT \
    dispat-release-tools gcloud "$@"
}

ssh_options() {
  printf '%s' "-i $dir/key -o IdentitiesOnly=yes -o UserKnownHostsFile=$dir/known_hosts -o StrictHostKeyChecking=yes -o ConnectTimeout=15 -o BatchMode=yes"
}

worker_ssh() {
  # The command text expands on this side on purpose: every caller writes the
  # values it wants the machine to see into the string it passes.
  # shellcheck disable=SC2046,SC2029
  ssh $(ssh_options) "$user@$(cat "$dir/ip")" "$@"
}

worker_scp() {
  # shellcheck disable=SC2046
  scp -q $(ssh_options) "$@"
}

require_state() {
  [ -f "$dir/ip" ] || fail "no worker state in $dir: run 'create' first"
}

# resolve_repository is the repository the worker polls, over https because
# the token reaches it there: DISPAT_CI_WORKER_REPOSITORY when it is set, the
# repository this workflow runs for otherwise, and outside a workflow the
# checkout's origin rewritten from its ssh spelling.
resolve_repository() {
  if [ -n "${DISPAT_CI_WORKER_REPOSITORY:-}" ]; then
    printf '%s' "$DISPAT_CI_WORKER_REPOSITORY"
    return
  fi
  if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
    printf '%s' "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY.git"
    return
  fi
  origin=$(git remote get-url origin 2>/dev/null) \
    || fail "no repository to poll: set DISPAT_CI_WORKER_REPOSITORY"
  printf '%s' "$origin" | sed -E 's#^(git@|ssh://git@)github\.com[:/]#https://github.com/#'
}

write_startup_script() {
  cat > "$dir/startup.sh" <<'EOF'
#!/bin/sh
# Runs as root at boot: the tools a task needs and the marker the job waits
# for. The worker itself is installed and started over ssh afterwards, as the
# user the ssh key metadata created. A fresh image runs its own package
# maintenance at boot and mirrors fail now and then, so the installs are
# retried, and a boot that still cannot install writes a failure marker with
# the reason so the job stops waiting and says why.
set -u
export DEBIAN_FRONTEND=noninteractive
install_tools() {
  apt-get update -q && apt-get install -y -q docker.io docker-buildx docker-compose-v2 git
}
tries=0
until install_tools; do
  tries=$((tries + 1))
  if [ "$tries" -ge 6 ]; then
    echo "package installation failed after $tries attempts" > /var/lib/dispat-worker-failed
    exit 1
  fi
  echo "package installation failed (attempt $tries); retrying" >&2
  sleep 20
done
touch /var/lib/dispat-worker-ready
EOF
}

await_host_keys() {
  deadline=$(( $(date +%s) + 600 ))
  while :; do
    keys=$(run_gcloud compute instances get-guest-attributes "$name" --zone "$(zone_of_instance)" \
      --query-path=hostkeys/ --format='value(key,value)' 2>/dev/null || true)
    if [ -n "$keys" ]; then
      ip=$(cat "$dir/ip")
      printf '%s\n' "$keys" | while IFS="$(printf '\t')" read -r type key; do
        [ -n "$type" ] && printf '%s %s %s\n' "$ip" "$type" "$key"
      done > "$dir/known_hosts"
      [ -s "$dir/known_hosts" ] && return
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      log "the instance published no host keys within ten minutes"
      return 1
    fi
    sleep 5
  done
}

# await_ready waits for the startup script's marker, and answers 1 rather
# than failing when the machine cannot be made ready, so that create may try
# another machine. A failure marker ends the wait at once; a machine that is
# still silent at the deadline has its startup log printed for the record.
await_ready() {
  deadline=$(( $(date +%s) + 900 ))
  until worker_ssh "test -e $ready_marker" 2>/dev/null; do
    if worker_ssh "test -e $failed_marker" 2>/dev/null; then
      log "the instance's startup script failed: $(worker_ssh "cat $failed_marker" 2>/dev/null)"
      return 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      log "the instance did not finish its startup script within fifteen minutes; its log follows"
      worker_ssh "sudo journalctl -u google-startup-scripts.service --no-pager 2>/dev/null | tail -n 40" >&2 \
        || log "(the instance could not be reached over ssh)"
      return 1
    fi
    sleep 10
  done
}

# create makes the instance and waits until it is ready to serve; a machine
# that never becomes ready is deleted and one more is made, in the zones
# after the one it came from, before the job is told there is none.
create() {
  mkdir -p "$dir"
  chmod 700 "$dir"
  [ -f "$dir/key" ] || ssh-keygen -q -t ed25519 -N '' -C "dispat-ci-worker" -f "$dir/key"
  printf '%s:%s\n' "$user" "$(cat "$dir/key.pub")" > "$dir/ssh-keys"
  write_startup_script
  if create_instance; then
    return
  fi
  log "replacing the instance with a fresh one"
  failed_zone=$(zone_of_instance)
  delete_instance
  zones=$(printf '%s' "$zones" | tr ',' '\n' | grep -v "^$failed_zone\$" | paste -sd, -)
  [ -n "$zones" ] || zones=us-central1-b
  create_instance || fail "no instance became ready to serve"
}

create_instance() {
  rm -f "$dir/ip" "$dir/zone" "$dir/machine"
  for machine in $(printf '%s' "$machines" | tr ',' ' '); do
  for zone in $(printf '%s' "$zones" | tr ',' ' '); do
    log "creating $machine instance $name in $zone"
    if run_gcloud compute instances create "$name" --zone "$zone" \
      --machine-type "$machine" \
      --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud \
      --boot-disk-size "${disk_gb}GB" --boot-disk-type pd-balanced \
      --no-service-account --no-scopes \
      --provisioning-model STANDARD \
      --max-run-duration "$lifetime" --instance-termination-action DELETE \
      --labels "dispat-ci=worker,run=$run_id" \
      --metadata enable-guest-attributes=TRUE,enable-oslogin=FALSE \
      --metadata-from-file "startup-script=$dir/startup.sh,ssh-keys=$dir/ssh-keys" \
      --format 'value(networkInterfaces[0].accessConfigs[0].natIP)' > "$dir/ip"; then
      printf '%s\n' "$zone" > "$dir/zone"
      printf '%s\n' "$machine" > "$dir/machine"
      break 2
    fi
    log "no $machine in $zone right now; trying the next"
  done
  done
  [ -f "$dir/zone" ] || fail "no zone of $zones could provide any of $machines"
  [ -s "$dir/ip" ] || fail "the instance was created without an external address"
  printf '%s\n' "$name" > "$dir/name"
  log "$(cat "$dir/machine") instance $name at $(cat "$dir/ip") in $(cat "$dir/zone"); waiting for its host keys"
  await_host_keys || return 1
  log "waiting for the startup script"
  await_ready || return 1
  log "ready"
}

start() {
  binary=${1:-}
  if [ -z "$binary" ] || [ ! -f "$binary" ]; then
    fail "start needs the path of a linux/amd64 dispat binary"
  fi
  : "${DISPAT_EXECUTION_SECRET:?DISPAT_EXECUTION_SECRET is required: the secret the orchestrator signs with}"
  : "${DISPAT_CI_WORKER_TOKEN:?DISPAT_CI_WORKER_TOKEN is required: the token the worker reaches the repository with}"
  repository=$(resolve_repository)
  case "$repository" in
    https://*) ;;
    *) fail "the worker reaches the repository over https with its token; $repository is not an https URL" ;;
  esac
  require_state
  worker_scp "$binary" "$user@$(cat "$dir/ip"):dispat"
  # The docker group takes effect on the next login, which is why the worker
  # starts in a session of its own below rather than in this one.
  worker_ssh "sudo install -m 0755 dispat /usr/local/bin/dispat && sudo usermod -aG docker $user \
    && mkdir -p ~/node ~/state && chmod 700 ~/node \
    && git config --global user.email worker@dispat.invalid && git config --global user.name 'dispat worker'"
  # The secret, the token, the repository and the commit travel one way:
  # over ssh, into files only this user reads, never on a command line.
  printf '%s' "$DISPAT_EXECUTION_SECRET" | worker_ssh "umask 077 && cat > ~/node/secret"
  printf '%s' "$DISPAT_CI_WORKER_TOKEN" | worker_ssh "umask 077 && cat > ~/node/github-token"
  printf '%s' "$repository" | worker_ssh "umask 077 && cat > ~/node/endpoint"
  # The job's build cache, for this machine as well as the runner. Every gate
  # is a buildx build that scripts/buildx-cache.sh gives the Actions cache
  # flags when GITHUB_ACTIONS is true, and the gha backend authenticates with
  # the ACTIONS_* values the runtime action exported into this job. Without
  # them a task placed here rebuilt and retested everything cold, whatever the
  # runner had cached. The backend also needs a docker-container builder, which
  # the default docker driver is not, so one is created first, in a login of
  # its own after the docker group change above. The values travel like the
  # signing secret, over ssh into a file only this user reads. A machine that
  # cannot create the builder, or a job with no Actions runtime, serves with no
  # cache rather than not at all.
  cache_env=""
  if [ -n "${ACTIONS_RUNTIME_TOKEN:-}" ]; then
    if worker_ssh "docker buildx inspect dispat-cache >/dev/null 2>&1 \
        || docker buildx create --name dispat-cache --driver docker-container --bootstrap >/dev/null" \
      && worker_ssh "docker buildx use dispat-cache"; then
      cache_env="GITHUB_ACTIONS='true'"
      for name in ACTIONS_RUNTIME_TOKEN ACTIONS_RESULTS_URL ACTIONS_CACHE_URL ACTIONS_CACHE_SERVICE_V2; do
        value=$(printenv "$name" || true)
        [ -z "$value" ] || cache_env="$cache_env
$name='$value'"
      done
      log "the worker shares the job's build cache"
    else
      log "no docker-container builder on the worker; it serves without the build cache"
    fi
  fi
  printf '%s\n' "$cache_env" | worker_ssh "umask 077 && cat > ~/node/cache.env"
  cat <<EOF | worker_ssh "cat > ~/node/dispat.json"
{
  "execution": {
    "role": "worker",
    "name": "$node",
    "endpoint": "$repository",
    "secretEnv": "DISPAT_EXECUTION_SECRET",
    "concurrency": ${DISPAT_CI_WORKER_CONCURRENCY:-2},
    "transfer": {"maxFiles": 200000, "maxBytes": 8589934592, "maxManifestBytes": 67108864, "timeout": 3600}
  },
  "logFormat": "json"
}
EOF
  # The commit this machine serves. A task's commands inherit the worker's
  # environment, and scripts/buildx-cache.sh stamps every coverage profile
  # with GITHUB_SHA when it is set and with the checkout's HEAD otherwise;
  # on this machine HEAD is the snapshot the node built from, so without the
  # variable the profiles it sends back would name a commit the job's own
  # profiles do not, and the coverage freshness gate would refuse the set.
  commit=${GITHUB_SHA:-$(git rev-parse HEAD)}
  printf '%s' "$commit" | worker_ssh "umask 077 && cat > ~/node/commit"
  # The launcher, written as it runs: nothing in it expands on this side. It
  # hands git the token as an extra header scoped to this repository's URL, so
  # the header is sent to that repository and to no other URL of the host,
  # and it never reaches a command line, a git config file or the log.
  cat <<'LAUNCHER' | worker_ssh "umask 077 && cat > ~/node/serve.sh && chmod 700 ~/node/serve.sh"
#!/bin/sh
# Written by scripts/ci-worker.sh start: serves this node until it is stopped.
set -eu
set -a
. "$HOME/node/cache.env"
set +a
endpoint=$(cat "$HOME/node/endpoint")
basic=$(printf 'x-access-token:%s' "$(cat "$HOME/node/github-token")" | base64 | tr -d '\n')
GIT_TERMINAL_PROMPT=0
GIT_CONFIG_COUNT=1
GIT_CONFIG_KEY_0="http.$endpoint.extraheader"
GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $basic"
DISPAT_EXECUTION_SECRET=$(cat "$HOME/node/secret")
GITHUB_SHA=$(cat "$HOME/node/commit")
export GIT_TERMINAL_PROMPT GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0 DISPAT_EXECUTION_SECRET GITHUB_SHA
exec dispat worker --root "$HOME/node" --state-dir "$HOME/state" --idle-timeout 0 --log-level debug
LAUNCHER
  worker_ssh "setsid -f ~/node/serve.sh > ~/worker.log 2>&1 < /dev/null"
  sleep 3
  worker_ssh "pgrep -x dispat >/dev/null && tail -n 3 ~/worker.log" \
    || fail "the worker did not stay up; its log follows: $(worker_ssh 'cat ~/worker.log' 2>/dev/null)"
  log "worker $node serving $repository"
}

# prune deletes the coordination branches this run's node left on the
# repository, with the job's own credential. The orchestrator closes every
# branch it created when the sweep ends, so this finds something only after a
# sweep that was interrupted or could not close; the pattern is this run's
# node, anchored on the date that follows it, so no other run's branch
# matches.
prune() {
  pattern="refs/heads/dispat-worker-$node-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-*"
  refs=$(git ls-remote --heads origin "$pattern" | awk '{print $2}')
  if [ -z "$refs" ]; then
    log "no coordination branch of $node is left"
    return
  fi
  # shellcheck disable=SC2046
  git push --quiet origin $(printf '%s\n' "$refs" | sed 's#^#:#') \
    || fail "the coordination branches of $node could not all be deleted"
  log "deleted $(printf '%s\n' "$refs" | wc -l | tr -d ' ') coordination branches of $node"
}

# collect copies the worker's log; a job that never had a machine, or was
# cancelled before one answered, has nothing to collect and says so.
collect() {
  dest=${1:-}
  [ -n "$dest" ] || fail "collect needs a destination folder"
  [ -f "$dir/ip" ] || { log "no worker state; nothing to collect"; return; }
  mkdir -p "$dest"
  worker_scp "$user@$(cat "$dir/ip"):worker.log" "$dest/worker.log" || log "no worker log to collect"
}

# delete_instance removes the instance and what the job knows about it,
# keeping the ssh key pair for a replacement. A job cancelled while create
# was still running may hold no state at all although the instance exists,
# so without state the instance is deleted by the name this run gives it,
# asked of every zone the run could have chosen.
delete_instance() {
  if [ -f "$dir/name" ]; then
    target=$(cat "$dir/name")
    candidate_zones=$(zone_of_instance)
  else
    log "no worker state; deleting $name by name wherever it exists, in case the job was cancelled mid-creation"
    target=$name
    candidate_zones=$(printf '%s' "$zones" | tr ',' ' ')
  fi
  for candidate_zone in $candidate_zones; do
    if run_gcloud compute instances delete "$target" --zone "$candidate_zone" --quiet 2>/dev/null; then
      log "deleted instance $target in $candidate_zone"
    elif [ -f "$dir/name" ]; then
      log "delete reported an error; the instance deletes itself after $lifetime in any case"
    fi
  done
  rm -f "$dir/name" "$dir/ip" "$dir/zone" "$dir/machine" "$dir/known_hosts"
}

delete() {
  delete_instance
  rm -f "$dir/key" "$dir/key.pub" "$dir/ssh-keys"
}

case "$command" in
  create) create ;;
  start) start "$@" ;;
  prune) prune ;;
  collect) collect "$@" ;;
  delete) delete ;;
  *) echo "unknown command: $command" >&2; exit 2 ;;
esac
