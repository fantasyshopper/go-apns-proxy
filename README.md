# APNS Proxy server

A Buffering proxy for sending APNS messages.

Usage:

    go-apns-proxy \
      -apns:cert="path/to/apns.cert" -apns:key="path/to/apns.key" \
      -sbox:cert="path/to/sandbox.cert" --sbox:key="path/to/sandbox.key" \
      -buffer=10000 \
      -listen=127.0.0.1:8765

    # then you can
    echo '{"device":"a1b2c3d4e5f6...","expiry":"3600","payload":"..."}' | nc 0 8765
