`centrifuge.js` is the unmodified browser distribution from npm `centrifuge@5.7.4` (MIT; license alongside it).

Source: https://github.com/centrifugal/centrifuge-js

Vendored locally so the embedded lobby needs neither a frontend build nor a CDN connection. To update, run `npm pack centrifuge@<version> --ignore-scripts` in a temporary directory, copy `package/dist/centrifuge.js` and `package/LICENSE`, and run the browser acceptance test.
