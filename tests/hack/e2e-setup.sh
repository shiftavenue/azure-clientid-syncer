#! /bin/bash -e
set -x

SCRIPT_DIR=$(realpath $(dirname "$0"))/..
for file in $SCRIPT_DIR/*.sh; do
  chmod +x $file
done

wait_on_delete="false"
delete_resources_afterwards="true"

while getopts w:d: flag; do
  case "${flag}" in
  w) wait_on_delete=${OPTARG} ;;
  d) delete_resources_afterwards=${OPTARG} ;;
  esac
done



function cleanup {
  $SCRIPT_DIR/cleanup-infrastructure.sh -w $wait_on_delete
}

if [ "$delete_resources_afterwards" = "true" ]; then
  trap cleanup EXIT
fi

$SCRIPT_DIR/provision-infrastructure.sh
# execute every script starting with test- in the tests directory
for file in $SCRIPT_DIR/test-*.sh; do
  $file
done