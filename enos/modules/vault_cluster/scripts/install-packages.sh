#!/bin/bash
# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: BUSL-1.1

set -ex -o pipefail

if [ "$PACKAGES" == "" ]
then
  echo "No dependencies to install."
  exit 0
fi

function retry {
  local retries=$1
  shift
  local count=0

  until "$@"; do
    exit=$?
    wait=$((2 ** count))
    count=$((count + 1))
    if [ "$count" -lt "$retries" ]; then
      sleep "$wait"
    else
      exit "$exit"
    fi
  done

  return 0
}

# Wait for cloud-init to finish so it doesn't race with any of our package
# installations
cloud-init status --wait

echo "Installing Dependencies: $packages"

# Check which package manager our Linux distro comes with, and use that to
# install packages.
if [ -n "$(command -v yum)" ]; then
  cd /tmp
  sudo yum -y install "$${packages[@]}"
elif [ -n "$(command -v apt)" ]; then
  cd /tmp
  sudo apt update
  sudo apt install -y "$${packages[@]}"
elif [ -n "$(command -v zypper)" ]; then
  # Note: for SLES 12.5 SP5, some packages are not offered in an official repo.
  # If the first install step fails, we instead attempt to register with PackageHub,
  # SUSE's third party package marketplace, and then find and install the package
  # from there.
  sudo zypper install --no-confirm "$${packages[@]}" || ( sudo SUSEConnect -p PackageHub/12.5/x86_64 && sudo zypper install --no-confirm "$${packages[@]}")
else
  echo "No package manager found."
  exit 1
fi
