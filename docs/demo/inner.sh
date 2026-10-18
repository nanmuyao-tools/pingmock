#!/bin/bash
# Runs inside pingmock's network. No root was needed to get here.
. "$(dirname "$0")/lib.sh"
sleep 1
run "./fastest 10.99.0.1 10.99.0.2 10.99.0.3" 2
comment "Now slow the chosen server down while everything keeps running:" 1.5
run "curl -s -X PUT \$PINGMOCK_API/rules/10.99.0.2 -d 200ms" 1.5
run "./fastest 10.99.0.1 10.99.0.2 10.99.0.3" 2
comment "...and make the new favourite unreachable:" 1.5
run "curl -s -X PUT \$PINGMOCK_API/rules/10.99.0.3 -d unreachable" 1.5
run "./fastest 10.99.0.1 10.99.0.2 10.99.0.3" 2.5
