# dcrmapper

Crawls the Decred network to build a world map displaying every publicly
accessible full node.

Uses <https://ip-api.com/> for IP geolocation.

## Running

```sh
go run . [-testnet] [-listen 127.0.0.1:8111] [-domain localhost]
```

## Development

Common tasks are wrapped in a `Makefile` — run `make help` to list them:

```sh
make run        # build the CSS (if stale) and the binary, then start the server
make css-watch  # rebuild the stylesheet on every template change
make check      # gofmt, go vet, golangci-lint and tests (the pre-commit gate)
```

`make tools` downloads the pinned [Tailwind](https://tailwindcss.com/) standalone
CLI into `./bin` (no Node.js required); the CSS targets fetch it automatically.

## Frontend

The UI is styled with [Tailwind CSS](https://tailwindcss.com/). The compiled
stylesheet at `public/css/tailwind.css` is committed, so no build step is needed
just to run the server. After changing classes in `templates/` or editing
`tailwind.input.css`, rebuild it with `make css`.
