#!/usr/bin/env bash

set -x -euo pipefail


up() {
    vagrant up

    # this is not ideal, see: https://stackoverflow.com/a/27327955
    vagrant ssh -c 'nohup ~/toy-socks5/toy-socks5 -global 2>&1 >/srv/wg.log & sleep 1'
    vagrant ssh -c 'tail -f /srv/wg.log'
}

down() {
    vagrant halt
}


if [ "$1" == "start" ]; then
    up
elif [ "$1" == "stop" ]; then
    down
else
    echo ""
fi
