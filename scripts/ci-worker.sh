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
#   sh scripts/ci-worker.sh endpoint        print the mailbox URL the orchestrator links to
#   sh scripts/ci-worker.sh git-ssh         print the GIT_SSH_COMMAND value that reaches it
#   sh scripts/ci-worker.sh collect <dir>   copy the worker's log into <dir>
#   sh scripts/ci-worker.sh delete          delete the instance (a missing one is not an error)
#
# Everything the steps share lives in DISPAT_CI_WORKER_DIR: the ephemeral ssh
# key pair, the instance's address, its host keys and the endpoint. The
# signing secret is DISPAT_EXECUTION_SECRET in the environment of `start`; it
# travels to the machine over ssh and is never written into instance
# metadata, which anyone with project access can read.
#
# gcloud runs on the host when it is installed, and otherwise inside the
# release tools image exactly as the infra and docs stages run it, with the
# Workload Identity credential files the auth step wrote reachable at their
# own paths. The instance carries no service account: it needs no cloud
# credential to serve tasks, and a task that ran on it could not acquire one.
set -eu

command=${1:-}
[ -n "$command" ] || { echo "usage: ci-worker.sh create|start|endpoint|git-ssh|collect|delete" >&2; exit 2; }
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
# The repository whose history seeds the mailbox, so the orchestrator's first
# push carries the working tree and not every object the checkout reaches.
# A repository the machine cannot clone (private, or no network) leaves the
# mailbox empty and the push carries everything, which is slower and correct.
seed=${DISPAT_CI_WORKER_SEED:-}
node=${DISPAT_CI_WORKER_NODE:-ci-worker}
user=dispat
mailbox_path=/home/$user/mailbox.git
ready_marker=/var/lib/dispat-worker-ready

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

resolve_seed() {
  [ -z "$seed" ] || { printf '%s' "$seed"; return; }
  if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
    printf '%s' "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY.git"
    return
  fi
  # A developer's origin is often an ssh remote the machine holds no key
  # for; a public GitHub repository is reachable at its https address.
  git remote get-url origin 2>/dev/null \
    | sed -E 's#^(git@|ssh://git@)github\.com[:/]#https://github.com/#' || true
}

write_startup_script() {
  cat > "$dir/startup.sh" <<'EOF'
#!/bin/sh
# Runs as root at boot: the tools a task needs and the marker the job waits
# for. The worker itself is installed and started over ssh afterwards, as the
# user the ssh key metadata created.
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q docker.io docker-buildx docker-compose-v2 git
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
    [ "$(date +%s)" -lt "$deadline" ] || fail "the instance published no host keys within ten minutes"
    sleep 5
  done
}

await_ready() {
  deadline=$(( $(date +%s) + 900 ))
  until worker_ssh "test -e $ready_marker" 2>/dev/null; do
    [ "$(date +%s)" -lt "$deadline" ] || fail "the instance did not finish its startup script within fifteen minutes"
    sleep 10
  done
}

create() {
  mkdir -p "$dir"
  chmod 700 "$dir"
  [ -f "$dir/key" ] || ssh-keygen -q -t ed25519 -N '' -C "dispat-ci-worker" -f "$dir/key"
  printf '%s:%s\n' "$user" "$(cat "$dir/key.pub")" > "$dir/ssh-keys"
  write_startup_script
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
  printf 'ssh://%s@%s%s\n' "$user" "$(cat "$dir/ip")" "$mailbox_path" > "$dir/endpoint"
  log "$(cat "$dir/machine") instance $name at $(cat "$dir/ip") in $(cat "$dir/zone"); waiting for its host keys"
  await_host_keys
  log "waiting for the startup script"
  await_ready
  log "ready"
}

start() {
  binary=${1:-}
  if [ -z "$binary" ] || [ ! -f "$binary" ]; then
    fail "start needs the path of a linux/amd64 dispat binary"
  fi
  : "${DISPAT_EXECUTION_SECRET:?DISPAT_EXECUTION_SECRET is required: the secret the orchestrator signs with}"
  require_state
  worker_scp "$binary" "$user@$(cat "$dir/ip"):dispat"
  seed_url=$(resolve_seed)
  # The docker group takes effect on the next login, which is why the worker
  # starts in a session of its own below rather than in this one.
  worker_ssh "sudo install -m 0755 dispat /usr/local/bin/dispat && sudo usermod -aG docker $user \
    && mkdir -p ~/node ~/state && chmod 700 ~/node \
    && git config --global user.email worker@dispat.invalid && git config --global user.name 'dispat worker' \
    && if [ ! -d $mailbox_path ]; then \
         if [ -n '$seed_url' ] && git clone -q --bare '$seed_url' $mailbox_path 2>/dev/null; then \
           echo 'mailbox seeded from $seed_url'; else rm -rf $mailbox_path; git init -q --bare $mailbox_path; \
           echo 'mailbox empty (no seed)'; fi; fi"
  printf '%s' "$DISPAT_EXECUTION_SECRET" | worker_ssh "umask 077 && cat > ~/node/secret"
  cat <<EOF | worker_ssh "cat > ~/node/dispat.json"
{
  "execution": {
    "role": "worker",
    "name": "$node",
    "endpoint": "file://$mailbox_path",
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
  worker_ssh "setsid -f sh -c 'GITHUB_SHA=$commit DISPAT_EXECUTION_SECRET=\$(cat ~/node/secret) exec dispat worker --root ~/node --state-dir ~/state --idle-timeout 0 --log-level debug' > ~/worker.log 2>&1 < /dev/null"
  sleep 3
  worker_ssh "pgrep -x dispat >/dev/null && tail -n 3 ~/worker.log" \
    || fail "the worker did not stay up; its log follows: $(worker_ssh 'cat ~/worker.log' 2>/dev/null)"
  log "worker $node serving $(cat "$dir/endpoint")"
}

endpoint() {
  require_state
  cat "$dir/endpoint"
}

git_ssh() {
  require_state
  printf 'ssh %s\n' "$(ssh_options)"
}

collect() {
  dest=${1:-}
  [ -n "$dest" ] || fail "collect needs a destination folder"
  require_state
  mkdir -p "$dest"
  worker_scp "$user@$(cat "$dir/ip"):worker.log" "$dest/worker.log" || log "no worker log to collect"
}

delete() {
  [ -f "$dir/name" ] || { log "nothing to delete"; return; }
  log "deleting instance $(cat "$dir/name") in $(zone_of_instance)"
  run_gcloud compute instances delete "$(cat "$dir/name")" --zone "$(zone_of_instance)" --quiet \
    || log "delete reported an error; the instance deletes itself after $lifetime in any case"
  rm -f "$dir/key" "$dir/key.pub" "$dir/name" "$dir/ip" "$dir/zone" "$dir/machine" "$dir/endpoint" "$dir/known_hosts" "$dir/ssh-keys"
}

case "$command" in
  create) create ;;
  start) start "$@" ;;
  endpoint) endpoint ;;
  git-ssh) git_ssh ;;
  collect) collect "$@" ;;
  delete) delete ;;
  *) echo "unknown command: $command" >&2; exit 2 ;;
esac
