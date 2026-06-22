# audit-app

Pinned to a deliberately old, vulnerable Log4j so `cappu audit` has advisories
to report; it scans the transitive graph (OSV.dev) and prints the dependency
tree that pulls each one in:

```sh
cappu audit                   # exits non-zero, lists the advisories
```
