# Media Processing HTTP API (FFmpeg API)

A **HTTP API** for processing video, audio, and images using **FFmpeg**.

## What it does

* Convert media formats (video ↔ audio, MP4 → GIF, WAV → MP3)
* Resize, trim, or transcode files
* Extract audio or frames from video
* Run media workflows without local file handling

You provide input files and FFmpeg commands; the API returns the results via **S3**, **Base64**, or a **direct HTTP stream**.

## Endpoint

`POST /v1/process`

### API Example

```bash
curl -X POST http://localhost:8080/v1/process \
  -H "Content-Type: application/json" \
  -d '{
    "input": {
      "input.mp4": { "http": "https://example.com/video.mp4" }
    },
    "commands": [
      ["-i", "input.mp4", "-vn", "output.mp3"]
    ],
    "output": { "base64": true }
  }'
```

## Request Structure

```json
{
  "s3Config": { ... },
  "input": { ... },
  "commands": [ ... ],
  "output": { ... }
}
```

## Inputs

A map of **filename → source** (used by FFmpeg).

Supported sources:

* `s3`: Objects stored in S3, referenced as `s3://bucket/key`
* `http`: Accessible URLs
* `base64`: Content provided as a Base64-encoded string
* `temporary`: A placeholder for indeterminate files that are not emitted as outputs

```json
{
  "input.mp4": {
    "http": "https://example.com/video.mp4"
  }
}
```

## FFmpeg Commands

An array of FFmpeg argument lists, executed sequentially.

```json
[
  ["-i", "input.mp4", "-vn", "output.mp3"]
]
```

Filenames reference input files or outputs from previous commands.

## Output Options

### Upload to S3

```json
{ "s3": "s3://my-bucket/outputs/" }
```

### Return Base64

```json
{ "base64": true }
```

### Stream via HTTP

```json
{ "inlineContentType": "audio/mpeg" }
```

> When streaming, the first output file is returned directly and no JSON is sent.

## JSON Response

```json
{
  "results": {
    "output.mp3": {
      "url": "...",
      "base64": "..."
    }
  }
}
```

## S3 Configuration

S3 access is configured **per request** using the `s3Config` object.

* Works with:
  * AWS S3
  * S3-compatible providers (MinIO, DigitalOcean Spaces, Cloudflare R2, etc.)
* The `endpoint` should **not** include a bucket name

* `useSSL` defaults to **true** if omitted

### Example

```json
{
  "s3Config": {
    "endpoint": "s3.amazonaws.com",
    "region": "us-east-1",
    "accessKey": "AKIA...",
    "secretKey": "SECRET...",
    "useSSL": true
  }
}
```

## Docker

* FFmpeg is bundled
* No external dependencies

### Image

```
ghcr.io/aureum-cloud/ffmpeg-api:latest
```

### Run

```bash
docker run -d -p 8080:8080 ghcr.io/aureum-cloud/ffmpeg-api:latest
```

API available at:

```
http://localhost:8080
```

## Scaling & Concurrency

Each request is handled independently by the HTTP server.

* Input files are fetched **in parallel** (HTTP, S3, Base64 decoding).
* FFmpeg is executed as a separate OS process per request.

Concurrency comes from handling multiple HTTP requests simultaneously. FFmpeg’s own multithreading is fully supported and can be controlled via standard flags such as:

```bash
-threads 0   # auto
-threads 4   # fixed
```

The service is stateless:

* No shared filesystem state
* No in-memory session data

This makes it easy to scale **horizontally** by running multiple instances behind a load balancer, for example using **Kubernetes**, or a managed container service.