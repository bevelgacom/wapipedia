# WAPipedia

**Wikipedia for WAP devices** - Access Wikipedia content on WAP-enabled mobile phones.

![WAPIPedia running on 3 differend Nokia phones](https://github.com/user-attachments/assets/924a2961-5f6d-4d26-b7f1-a486e30651e4)


WAPipedia is a server that uses Wikipedia ZIM files to serve articles in WML format, making them accessible on WAP 1.0 devices. Bridging the knowledge gap to old mobile devices and very low bandwidth connections.

This project is part of [Bevelgacom](https://github.com/bevelgacom) a Retro ISP focussed on re-building the WPA internet.

The project uses [Kiwix ZIM files](https://download.kiwix.org/zim/wikipedia/) for offline Wikipedia content to not put load on Wikipedia servers and allow for building an off-internet WAP server box.

## Public Instance

You can try this out on http://wiki.bevelgacom.be/

## Quick Start

### 1. Download Wikipedia Dump

First, download a Wikipedia dump. For testing, use the "top100" dump (~313MB):

```bash
./wapipedia download -lang top100 -dest ./data
```

### 2. Start the Server

```bash
./wapipedia serve --zim ./data/wikipedia_en_100_2025-10-01.zim -port 8080
```

### 3. Access WAPipedia

Open your WAP browser and navigate to:
```
http://your-server:8080/
```

## CLI Commands

```bash
# Start the server
wapipedia serve [-zim path/to/file.zim] [-port 8080]

# Download a Wikipedia dump
wapipedia download -lang <language> -dest <directory>

# List available dumps
wapipedia list

# Show help
wapipedia help
```

## Configuration

### Environment Variables

- `WAPIPEDIA_ZIM` - Path to the ZIM file to use

## Building

```bash
go build ./cmd/wapipedia
```

## Docker

```bash
docker build -t wapipedia .
docker run -p 8080:8080 -v ./data:/data -e WAPIPEDIA_ZIM=/data/wikipedia_en_100_2025-10.zim wapipedia
```
