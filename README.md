# toy-socks5
Toy sock5 implementation

## Run

- IPv4 only
- `select.select` is very slow ...

```bash
python server.py

curl -v  --socks5 127.0.0.1:1081 https://tools.ietf.org/html/rfc1928
```
