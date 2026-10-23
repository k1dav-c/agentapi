# Embedded chat assets

`chat/marker` keeps the embedded asset directory nonempty so `go build` works before the chat UI
assets are generated. Embedding an empty directory would fail.
