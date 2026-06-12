# dcrmapper

Crawls the Decred network to build a world map displaying every publicly
accessible full node.

Uses <https://ip-api.com/> for IP geolocation.

## Running

```sh
go run . [-testnet] [-listen 127.0.0.1:8111] [-domain localhost]
```

## Frontend

The UI is styled with [Tailwind CSS](https://tailwindcss.com/). The compiled
stylesheet at `public/css/tailwind.css` is committed, so no build step is needed
just to run the server.

If you change classes in `templates/` or edit `tailwind.input.css`, rebuild the
stylesheet with the Tailwind standalone CLI:

```sh
tailwindcss -i tailwind.input.css -o public/css/tailwind.css --minify
```

During development you can keep it rebuilding on change with `--watch`.
