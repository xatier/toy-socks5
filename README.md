# toy-socks5

Toy sock5 implementation of [RFC 1928](https://tools.ietf.org/html/rfc1928).

- IPv4 only, IPv6 ... not implemented fully

- The only difference between `socks5` and `socks5h` is hostname resolution. `curl(1)` manual has the following:

```text
--socks5-hostname: Use the specified SOCKS5 proxy (and let the proxy resolve the host name).

--socks5: Use the specified SOCKS5 proxy - but resolve the host name locally.
```

## Run

- start the socks5 proxy

```bash
# python version
python server.py

# Go version
go build && ./toy-socks5
```

- test with curl

```bash
# local resolution
curl -v --socks5 127.0.0.1:1081 https://tools.ietf.org/html/rfc1928
curl -v --proxy 'socks5://127.0.0.1:1081' 'https://tools.ietf.org/html/rfc1928'

# remote resolution
curl -v --socks5-hostname 127.0.0.1:1081 https://tools.ietf.org/html/rfc1928
curl -v --proxy 'socks5h://127.0.0.1:1081' 'https://tools.ietf.org/html/rfc1928'
```

## TODO

- Full IPv6 support

## WON'T DO

- Other authentication methods (GSSAPI and username/password)

- Other socks5 commands (bind and UDP associate)
