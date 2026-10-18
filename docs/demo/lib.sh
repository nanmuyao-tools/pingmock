# Shared by the demo driver scripts: show a prompt, "type" a command, run it.
# Every line of output in the recording is produced by the command shown.

type_cmd() {
  printf '\033[1;32m$\033[0m '
  local s=$1 i
  for ((i = 0; i < ${#s}; i++)); do
    printf '%s' "${s:i:1}"
    sleep 0.035
  done
  sleep 0.6
  printf '\n'
}

run() {
  type_cmd "$1"
  eval "$1"
  sleep "${2:-1.5}"
}

comment() {
  printf '\033[2m# %s\033[0m\n' "$1"
  sleep "${2:-1.2}"
}
