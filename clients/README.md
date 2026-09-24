# Go lobby example

From the lobby component root, run a host application plus Centrifugo, then create or join a lobby with the optional reference client:

```sh
go run ./clients -name Ada
go run ./clients -name Grace -join-code ABCDEFGH
```

Use `-api-url`, `-game-key`, `-name`, and `-join-code` to select the public endpoint and lobby flow. The example creates a guest credential, requests a Centrifugo connection token, subscribes to the returned room channel, and heartbeats only while connected. Ctrl-C releases this process's lease; it does not leave the durable lobby membership.

The production-oriented Python wrapper and CLI live under `clients/lobby-python/`; this Go command remains a compact manual reference.
