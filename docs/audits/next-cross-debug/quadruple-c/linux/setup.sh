#!/usr/bin/env bash
# Install the Linux Go toolchain and stage a copy of the repository on the WSL filesystem.
set -euo pipefail

if [ ! -x /usr/local/go/bin/go ]; then
  echo "downloading go1.27.4 linux-amd64"
  cd /root
  curl -sSLo go.tgz https://go.dev/dl/go1.27.4.linux-amd64.tar.gz
  ls -l go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf go.tgz
  rm -f go.tgz
fi
/usr/local/go/bin/go version

# Stage the repository on ext4: the 9p mount is correct but slow to build on.
if [ ! -d /root/jjb ]; then
  echo "staging the repository"
  mkdir -p /root/jjb
  rsync -a --exclude=.git --exclude=build /mnt/c/Deepseek/JiejieBox/ /root/jjb/
fi
ls /root/jjb | head -n 5
