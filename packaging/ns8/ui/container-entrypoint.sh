#!/bin/bash

if [[ -z "$1" ]]; then
    echo "Missing parameter: append 'watch' or 'build'"
elif [[ "$1" = "watch" ]]; then
    yarn install && yarn watch
elif [[ "$1" == "build" ]]; then
    yarn install && yarn build
else
    echo "Parameter not recognized: '$1'. Only 'watch' or 'build' are allowed"
fi
