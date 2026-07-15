// Package webrtc implements the WebRTC session manager for the rbi-go server.
// It mirrors the C# WebRtcSessionManager, acting as the answerer in the
// browser-is-offerer/server-is-answerer signaling model:
//   - A single HTTP round-trip carries the full offer and the complete answer
//     (no trickle ICE, no WebSocket).
//   - A send-only VP8 video track is added before negotiation so the answer
//     accepts the browser's recvonly video section.
//   - The pre-negotiated data channel (id=1, label="input-events") is wired
//     up before SetRemoteDescription so both sides agree on the channel without
//     a dedicated SDP negotiation round-trip.
//   - ICE candidates are gathered to completion before the answer SDP is
//     returned, so no trickle is needed.
//   - Host-candidate and session-level connection addresses in the answer SDP
//     are rewritten to the configured AdvertisedIp so browsers outside the
//     container can reach the published UDP port range.
package webrtc

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"rbi-go/internal/config"

	pion "github.com/pion/webrtc/v3"
)

// inputChannelID is the pre-negotiated data channel id shared between the
// client (wwwroot/index.html: `{ negotiated: true, id: 1 }`) and the server.
// Both sides must use the same value; changing one without the other breaks
// the data channel entirely.
const inputChannelID = uint16(1)

// inputChannelLabel is the human-readable name for the data channel. Matches
// the C# WebRtcSessionManager ("input-events") and index.html.
const inputChannelLabel = "input-events"

// vp8StreamID and vp8TrackID are the RTP stream/track identifiers embedded in
// the answer SDP. They are arbitrary but stable so the browser can correlate
// the SDP section with the actual RTP stream.
const (
	vp8StreamID = "rbi-video-stream"
	vp8TrackID  = "rbi-video-track"
)

// hostCandidateRe matches a=candidate lines for host-type candidates, capturing
// the prefix and suffix around the address field so we can substitute the
// advertised IP without disturbing the rest of the attribute.
// Example line: "a=candidate:1 1 udp 2130706431 172.17.0.2 40000 typ host generation 0"
var hostCandidateRe = regexp.MustCompile(`(a=candidate:\S+ \d+ \S+ \d+ )(\S+)( \d+ typ host)`)

// connectionAddrRe matches the session/media-level connection address line so
// it can be rewritten alongside the host candidates.
// Example line: "c=IN IP4 172.17.0.2"
var connectionAddrRe = regexp.MustCompile(`(c=IN IP4 )(\S+)`)

// Session holds the active pion peer connection and the media/data handles that
// Part 11 (video pipeline + input forwarding) will wire up once the connection
// reaches the "connected" state. Part 9 creates and returns a Session; Part 11
// attaches callbacks and drives the media pipeline.
type Session struct {
	// PC is the underlying pion peer connection. Part 11 registers
	// OnConnectionStateChange on this to start/stop the rendering session.
	PC *pion.PeerConnection

	// VideoTrack is the send-only VP8 track. Part 11 calls WriteSample on
	// this to deliver encoded VP8 frames to the browser.
	VideoTrack *pion.TrackLocalStaticSample

	// InputChannel is the pre-negotiated data channel (id=1). Part 11
	// registers OnMessage on this to receive browser input events (mouse,
	// keyboard) and forward them to the headless browser.
	InputChannel *pion.DataChannel
}

// Manager creates WebRTC answerer sessions. It is a singleton in the server
// process (one Manager, many concurrent sessions). Each call to CreateSession
// produces an independent pion.PeerConnection with its own UDP sockets inside
// the configured port range. Mirrors the C# WebRtcSessionManager role (minus
// the browser/video pipeline, which is Part 11's responsibility).
type Manager struct {
	// cfg is the WebRtc config section (AdvertisedIp, UdpPortStart, UdpPortEnd).
	cfg *config.WebRtcConfig
}

// NewManager constructs a Manager backed by the given WebRtc config section.
// cfg must outlive the Manager (typically both are singletons in main.go).
func NewManager(cfg *config.WebRtcConfig) *Manager {
	return &Manager{cfg: cfg}
}

// CreateSession accepts a browser WebRTC offer SDP string and performs the full
// answerer negotiation:
//  1. Constructs a pion RTCPeerConnection with the UDP port range from config.
//  2. Adds a send-only VP8 video track pre-answer (so the SDP answer accepts
//     the browser's recvonly video section).
//  3. Creates the pre-negotiated data channel (id=1, label="input-events").
//  4. Sets the remote description (the browser's offer).
//  5. Creates the answer and waits for ICE gathering to complete (no trickle).
//  6. Rewrites host-candidate and c= addresses to the configured AdvertisedIp.
//
// Returns the answer SDP ready to send back to the browser and a Session whose
// VideoTrack and InputChannel can be driven by Part 11's video/input pipeline.
// The caller is responsible for closing Session.PC when done (e.g. on
// connection-state change to disconnected/failed/closed).
//
// ctx is used only to bound the ICE-gathering wait; cancelling it closes the
// peer connection and returns an error.
func (m *Manager) CreateSession(ctx context.Context, offerSDP string) (answerSDP string, sess *Session, err error) {
	// Build the pion API with a SettingEngine that pins ICE/RTP UDP sockets to
	// the configured port range. Without this, pion picks ephemeral OS ports
	// that can't be statically published in a Docker container.
	var se pion.SettingEngine
	if portErr := se.SetEphemeralUDPPortRange(uint16(m.cfg.UdpPortStart), uint16(m.cfg.UdpPortEnd)); portErr != nil {
		return "", nil, fmt.Errorf("webrtc: set UDP port range [%d,%d]: %w",
			m.cfg.UdpPortStart, m.cfg.UdpPortEnd, portErr)
	}

	// pion.NewAPI defaults to an empty MediaEngine (no registered codecs) unless
	// one is explicitly supplied. Without VP8 (and the other default codecs)
	// registered, offer/answer negotiation for the video m-line can never
	// converge, failing with pion's "excessive retries" error.
	mediaEngine := &pion.MediaEngine{}
	if mediaErr := mediaEngine.RegisterDefaultCodecs(); mediaErr != nil {
		return "", nil, fmt.Errorf("webrtc: register default codecs: %w", mediaErr)
	}
	api := pion.NewAPI(pion.WithSettingEngine(se), pion.WithMediaEngine(mediaEngine))

	// No STUN/TURN: only host candidates are used. The AdvertisedIp rewrite
	// below makes them reachable from outside the container. ICEServers is
	// intentionally empty, mirroring the C# configuration: null configuration
	// means host-only candidates.
	pc, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}

	// Ensure the peer connection is closed if we return an error below — the
	// caller only gets a Session to close on the success path.
	defer func() {
		if err != nil {
			if closeErr := pc.Close(); closeErr != nil {
				slog.Warn("webrtc: close peer connection after setup error", "err", closeErr)
			}
		}
	}()

	// Create the VP8 send-only track. This must be added via
	// AddTransceiverFromTrack before SetRemoteDescription so the SDP answer
	// includes a sendonly video section that pairs with the browser's recvonly
	// section. Mirrors C# addTrack(videoTrack) before setRemoteDescription.
	videoTrack, err := pion.NewTrackLocalStaticSample(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8},
		vp8TrackID,
		vp8StreamID,
	)
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: new VP8 track: %w", err)
	}

	if _, err = pc.AddTransceiverFromTrack(videoTrack, pion.RTPTransceiverInit{
		Direction: pion.RTPTransceiverDirectionSendonly,
	}); err != nil {
		return "", nil, fmt.Errorf("webrtc: add VP8 transceiver: %w", err)
	}

	// Create the pre-negotiated data channel before SetRemoteDescription.
	// Both sides agree on id=1 and label="input-events" out-of-band (via
	// index.html's createDataChannel call and this server-side creation),
	// avoiding a dedicated SDP m= section for the data channel while still
	// allowing SCTP negotiation to complete once the DTLS transport is up.
	// Mirrors C# createDataChannel("input-events", { negotiated: true, id: 1 }).
	negotiated := true
	channelID := inputChannelID
	inputChannel, err := pc.CreateDataChannel(inputChannelLabel, &pion.DataChannelInit{
		Negotiated: &negotiated,
		ID:         &channelID,
	})
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: create data channel: %w", err)
	}

	// Set the remote description (the browser's offer). This must come after
	// adding the transceiver and data channel so pion can match the incoming
	// m= sections correctly.
	offer := pion.SessionDescription{
		Type: pion.SDPTypeOffer,
		SDP:  offerSDP,
	}
	if err = pc.SetRemoteDescription(offer); err != nil {
		return "", nil, fmt.Errorf("webrtc: set remote description: %w", err)
	}

	// Create the answer. The returned SDP does not yet have candidates — those
	// are populated asynchronously during ICE gathering, which starts once
	// SetLocalDescription is called.
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: create answer: %w", err)
	}

	// Subscribe to the gathering-complete signal BEFORE SetLocalDescription so
	// we don't miss the transition in case gathering completes immediately.
	// Mirrors C# X_WaitForIceGatheringToComplete = true in RTCAnswerOptions.
	gatherComplete := pion.GatheringCompletePromise(pc)

	if err = pc.SetLocalDescription(answer); err != nil {
		return "", nil, fmt.Errorf("webrtc: set local description: %w", err)
	}

	// Block until ICE gathering is done or the context is cancelled. After
	// this point pc.LocalDescription().SDP contains all host candidates.
	select {
	case <-gatherComplete:
		slog.Debug("webrtc: ICE gathering complete")
	case <-ctx.Done():
		return "", nil, fmt.Errorf("webrtc: ICE gathering cancelled: %w", ctx.Err())
	}

	// Retrieve the finalized local description (with fully populated candidates).
	localDesc := pc.LocalDescription()
	if localDesc == nil {
		return "", nil, fmt.Errorf("webrtc: local description is nil after gathering")
	}

	// Rewrite host-candidate and session-level c= addresses to the advertised
	// IP so a browser outside the Docker container can reach the published
	// UDP port range. Mirrors C# RewriteHostCandidateAddresses.
	finalSDP := rewriteHostCandidates(localDesc.SDP, m.cfg.AdvertisedIp)

	slog.Info("webrtc: session created",
		"advertisedIp", m.cfg.AdvertisedIp,
		"portRange", fmt.Sprintf("%d-%d", m.cfg.UdpPortStart, m.cfg.UdpPortEnd),
	)

	return finalSDP, &Session{
		PC:           pc,
		VideoTrack:   videoTrack,
		InputChannel: inputChannel,
	}, nil
}

// rewriteHostCandidates substitutes the container-internal IP in host-type
// ICE candidates and the session-level c= connection address with the
// configured advertised IP, making the answer SDP routable from outside the
// container. Only host candidates are touched — no STUN/TURN is configured,
// so srflx/relay candidates are never present. Mirrors C#
// RewriteHostCandidateAddresses.
//
// If advertisedIP is empty the SDP is returned unchanged (pion's auto-detected
// local IP is kept as-is).
func rewriteHostCandidates(sdp, advertisedIP string) string {
	if advertisedIP == "" {
		return sdp
	}
	out := connectionAddrRe.ReplaceAllString(sdp, "${1}"+advertisedIP)
	out = hostCandidateRe.ReplaceAllString(out, "${1}"+advertisedIP+"${3}")
	return out
}
