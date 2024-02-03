#! /bin/bash -e
set -x

wait_on_delete="false"

while getopts w: flag; do
  case "${flag}" in
  w) wait_on_delete=${OPTARG} ;;
  esac
done

source $(realpath $(dirname "$0"))/tests.env

if [ "$wait_on_delete" = "true" ]; then
  echo "waiting for azure rg $RG to be deleted ..."
  az group delete -n $RG --yes
  echo "azure rg $RG has been deleted"
else
  echo "cleaning up azure rg $RG ..."
  az group delete -n $RG --no-wait --yes
  echo "cleaning up azure rg $RG in the background"
fi

rm $(realpath $(dirname "$0"))/tests.env