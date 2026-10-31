#!/bin/bash
# Recorded by record.sh, which builds ghostwire and fastest and puts them on PATH.
# Shows the ghostwire exec command line, then runs the inner script in its
# network. The shell shown is "bash"; the one run is "bash inner.sh", which
# types the same commands a person would type into that shell.
here=$(dirname "$0")
. "$here/lib.sh"
sleep 0.8
comment "fastest pings each server and picks the fastest one. Run it where ghostwire answers the pings:" 2
type_cmd "ghostwire exec -rule 10.99.0.1=80ms -rule 10.99.0.2=20ms -rule 10.99.0.3=50ms -- bash"
ghostwire exec -rule 10.99.0.1=80ms -rule 10.99.0.2=20ms -rule 10.99.0.3=50ms -- bash "$here/inner.sh"
sleep 2
