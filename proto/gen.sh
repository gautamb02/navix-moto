#!/bin/sh
# Regenerate Go code from proto/aap/*.proto. Needs protoc and protoc-gen-go on PATH.
set -e
cd "$(dirname "$0")/aap"
OPTS=""
for f in *.proto; do OPTS="$OPTS --go_opt=M$f=navix-moto/proto/pb/aap;aap"; done
protoc -I. --go_out=../.. --go_opt=module=navix-moto $OPTS *.proto
