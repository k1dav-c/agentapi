# Embedded chat assets

The `chat/marker` file keeps the embedded asset directory nonempty, allowing the AgentAPI binary to
build before chat UI assets are generated. Without it, `go build` would fail when the directory
contains no files.
