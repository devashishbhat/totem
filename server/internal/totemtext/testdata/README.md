# Cross-language text vectors

`vectors.json` is generated from the Rust core, which is the implementation the
SDK uses. The Go server has to agree with it exactly: a fingerprint computed in
the SDK is how the server recognises a memory, and a placeholder computed in the
SDK is how the dashboard shows a value flowing into a tool argument.

Regenerate after changing any normalization, fingerprinting, redaction or
canonical-JSON rule:

    make text-vectors

Then run `go test ./internal/totemtext/` and `cargo test` in `sdk/core`, and
look at the diff: a change here changes the identity of every memory already
stored.
