# navix-moto: Stage 1 plan (laptop ↔ phone)

Goal: a Go program on the laptop connects to the phone, convinces Android Auto it's a motorcycle screen, and shows the live Google Maps video. Each milestone ends with a visible check.

## Milestone 0: Set up phone and laptop
- **Phone:** update Android Auto, unlock developer settings (tap Version 10 times), tap ⋮ → **Start headunit server**, set Android Auto's battery use to Unrestricted, turn on a 2.4 GHz hotspot.
- **Laptop:** join the phone's hotspot. Install Go, `protoc` with the Go plugin, and FFmpeg (for `ffplay`).
- **Find the phone's IP:** on a phone hotspot, the phone is the default gateway (`ip route` on Linux/Mac, `ipconfig` on Windows).
- **Done when:** `nc -vz <phone-ip> 5277` reports the port is open.

## Milestone 1: Connect and exchange versions
- Open a TCP connection to port 5277.
- Write the **framer**. Each frame has a 4-byte header:
  - channel ID (1 byte)
  - flags (1 byte: first/last fragment, control or channel message, encrypted)
  - payload length (2 bytes, big-endian)
  - The first fragment of a large message carries 4 extra bytes with the total size.
  - Inside the payload, the first 2 bytes are the message ID.
- Send a **version request** on channel 0 and read the response.
- Log every frame as hex from day one.
- **Done when:** the log shows the phone's version response with status OK.

## Milestone 2: The encrypted handshake
- The laptop acts as the TLS client. TLS bytes travel inside "SSL handshake" messages on channel 0, not over the raw socket.
- Write an adapter implementing `net.Conn` that wraps outgoing TLS bytes into those messages and unwraps incoming ones, so `crypto/tls` runs on top unchanged.
- Use the reference head unit certificate and key published by aasdk / Open Headunit (MiniNav uses the same).
- After the handshake, send **auth complete**. From then on, frames are encrypted: decrypt with the TLS session before parsing.
- **Done when:** the handshake completes and the phone's next message is a service discovery request.

## Milestone 3: Describe your "car"
- Reply to service discovery with a protobuf message listing:
  - **Video:** 800×480 at 30 fps, H.264 (smallest Android Auto size; scale down on the ESP32 later).
  - **Input:** touch (optional for now).
  - **Sensors:** driving status and night mode.
  - **Motorcycle mode:** set the same field MiniNav sets for its Motorcycle option, for two-wheeler routes.
- **Done when:** the phone starts sending channel-open requests.

## Milestone 4: Open channels and keep the link alive
- Accept each channel-open request.
- Answer pings, navigation-focus and audio-focus requests.
- When the sensor channel opens, send a driving-status event and a night-mode event.
- **Done when:** the phone sends a media setup message on the video channel.

## Milestone 5: Receive the map video
- Reply to media setup with a config response, then send video focus so the phone starts streaming.
- Receive H.264 frames and send an **ACK** for each (the phone stops if too many are unacknowledged).
- Write frames to `out.h264` and pipe them into `ffplay -f h264 -` for a live view.
- **Done when:** navigation on the phone plays live on the laptop.

## Milestone 6: Make it reliable and measure it
- Handle disconnects, reconnect automatically, send a proper shutdown on exit.
- Check that Maps offers two-wheeler routes.
- Log frame size, bitrate and fps (sizing data for the ESP32).
- **Done when:** a 30-minute test runs without dropping.

## Milestone 7: Prepare for the ESP32
- Write `docs/protocol.md` describing every message handled, in your own words.
- Record the largest frame size and the buffer sizes needed.
- This becomes the blueprint for the C version in Stage 3.

## Project layout
```
navix-moto/
├── cmd/navix/main.go       entry point, flags (phone IP, output file)
├── internal/transport/     TCP connection and the framer
├── internal/tlsconn/       net.Conn adapter for the handshake
├── internal/aap/           control channel: version, auth, discovery, ping, focus
├── internal/channels/      video, sensor, input handlers
├── proto/                  .proto files and generated Go code
└── docs/protocol.md        notes on the protocol
```

## References
- **aasdk:** message IDs, flags and the order of steps.
- **Open Headunit (HeadUnit Revived):** working wireless head unit; MiniNav's protocol source.
- **MiniNav `aap.cpp`:** closest to this project, already working over the headunit server.

Verify every message ID and field against these, since Google doesn't publish the protocol.

**Licence:** aasdk is GPL and MiniNav is AGPL. To reuse their `.proto` files or code, release navix-moto under GPL-3.0. For a different licence, write everything yourself.

## Likely sticking points
- **Fragmented messages:** large messages split across frames. Get the framer right first.
- **Handshake stalls:** stop and restart the headunit server on the phone.
- **Video never starts:** usually an unanswered request. Reply to everything the phone sends.