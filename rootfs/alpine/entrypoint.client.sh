#!/bin/sh

if [ $# -eq 0 ] ; then
  set -- "help"
fi

if [ "$1" = "sh" ] ; then
  exec "$@"
else
  exec /opt/easegateway/bin/easegateway-client "$@"
fi
