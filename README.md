# 👻 TrollSSH

A fake SSH server that accepts any login and plays back video as colored
ASCII/Unicode art at whoever connects to it

![SSH Demo](assets/ssh.webp)

## Features

- **Accept any credentials**
- **Colored ASCII/Unicode video playback** - resized live to the client's
  terminal, using a configurable character ramp, defaults to the Unicode block-character preset
- **Automatic color detection** - renders 24-bit truecolor, 256-color, or
  plain grayscale depending on what the connecting client reports supporting
- **Multiple frame sets** - clients get a random one, arrow keys switch between
  them (configurable)
- **Honeypot extras** - optional credential logging, per-IP and global
  connection limits, handshake timeout, customizable banner / fake login /
  goodbye text


## Quick start

Generate a frame set from a video through container image

```sh
docker run --rm -v ./video.mp4:/home/app/video.mp4 -v ./frames:/home/app/frames \
    ghcr.io/yuzuzensai/trollssh:v1.1.1 trollssh --generate --video video.mp4 --resolution 512
```

This writes `frames/<name>.tsf`, a simple container of color JPEG frames plus
the source fps. You can drop as many `.tsf` files into `frames/` as you like.
The server loads all of them.

Run the server with [`docker-compose.yaml`](docker-compose.yaml)

```sh
cp .env.example .env
docker compose up -d
```

`./data` persists host keys and text assets.

`./frames` holds the `.tsf` frame sets.

Then try it:

```sh
ssh anyone@localhost
```

## Configuration

Server configuration is via environment variables, loaded from a `.env` file
if one exists (see [`.env.example`](.env.example) for the full annotated
list). Durations are in milliseconds.

Host keys (`data/id_rsa`, `data/id_ed25519`) are generated on first run and
reused afterwards.

Frame generation is configured with flags:

| Flag                 | Default | Description                                        |
| -------------------- | ------- | -------------------------------------------------- |
| `--generate`, `-g`   |         | Generate a `.tsf` frame set instead of serving     |
| `--video`, `-v`      |         | Source video path                                  |
| `--resolution`, `-r` | `512`   | Stored frame max dimension in pixels. Higher = sharper but bigger `.tsf` files and slower rendering |

## Customization

Optional text files in `data/` (created next to the binary):

| File                 | Shown                                               | 
| -------------------- | --------------------------------------------------- |
| `data/banner.txt`    | As the SSH banner, before authentication            |
| `data/fakelogin.txt` | Right after "login", before playback                |
| `data/goodbye.txt`   | When the session ends after `MAX_LOOP` playthroughs |

## Development

Requirements: Go 1.25+ and `ffmpeg` / `ffprobe` on `PATH` (only for
`--generate`).

The entry point lives in [`cmd/trollssh`](cmd/trollssh); the packages it wires
together are under [`internal/`](internal) (`config`, `logx`, `render`, `tsf`,
`sshserver`).

```sh
go run ./cmd/trollssh --generate --video video.mp4 --resolution 512
go run ./cmd/trollssh
```

Or build a binary:

```sh
go build -o trollssh ./cmd/trollssh
./trollssh
```

CI runs the following checks on every push:

```sh
gofmt -l .                        # format
go vet ./...                      # vet
golangci-lint run ./...           # lint, see https://golangci-lint.run
go test ./...                     # tests
```

To run them automatically before each commit, install
[lefthook](https://github.com/evilmartians/lefthook) and `golangci-lint`, then:

```sh
lefthook install
```

## License

[MIT](LICENSE)
