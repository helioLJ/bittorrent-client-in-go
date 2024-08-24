# BitTorrent Client

A simple BitTorrent client implemented in Go.

## Features

- Support for both .torrent files and magnet links
- Peer discovery through trackers
- File downloading and piece management
- Basic logging and debugging capabilities

## Installation

To install the BitTorrent client, make sure you have Go installed on your system. Then, clone this repository and build the project:

```
git clone https://github.com/yourusername/bittorrent-client.git
cd bittorrent-client
go build ./cmd/bittorrent-client
```

## Usage

To use the BitTorrent client, run the compiled binary with a .torrent file or magnet link as an argument:

```
./bittorrent-client <torrent_file_or_magnet_link> [output_file]
```

Options:
- `-debug`: Enable debug logging

Example:
```
./bittorrent-client ubuntu-22.04.2-desktop-amd64.iso.torrent
```

or

```
./bittorrent-client "magnet:?xt=urn:btih:TORRENT_HASH&dn=Ubuntu+ISO"
```

## Project Structure

The project is organized into several packages:

- `cmd/bittorrent-client`: Contains the main application entry point
- `internal/file`: Handles file operations and piece management
- `internal/torrent`: Implements torrent-related functionality
- `internal/tracker`: Manages communication with trackers
- `pkg/logger`: Provides logging utilities

## Main Components

1. Torrent Parsing: The client can parse both .torrent files and magnet links (see lines 65-76 in the main.go file).

2. Tracker Communication: The client communicates with trackers to discover peers (implemented in the `internal/tracker` package).

3. Peer Connection: The client establishes connections with peers and manages the download process.

4. File Management: The client handles writing downloaded pieces to the output file (implemented in the `internal/file` package).

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License.

## Acknowledgements

This project uses the following open-source libraries:
- github.com/anacrolix/torrent
- github.com/anacrolix/dht

## TODO

- Implement DHT for trackerless torrents
- Add support for uploading (seeding)
- Improve error handling and recovery
- Implement more advanced piece selection algorithms
- Add a user interface (CLI or GUI)