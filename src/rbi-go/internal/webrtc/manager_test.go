package webrtc

import (
	"context"
	"strings"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v3"

	"rbi-go/internal/config"
)

// === Tier 1: SDP Rewrite Function Tests ===

// TestRewriteHostCandidates_EmptyAdvertisedIP_NoOp verifies that empty advertisedIP returns SDP unchanged.
func TestRewriteHostCandidates_EmptyAdvertisedIP_NoOp(t *testing.T) {
	sdp := `v=0
o=- 123 456 IN IP4 192.168.1.1
c=IN IP4 192.168.1.1
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host generation 0`

	result := rewriteHostCandidates(sdp, "")
	if result != sdp {
		t.Errorf("expected SDP unchanged with empty advertisedIP, got %q", result)
	}
}

// TestRewriteHostCandidates_SingleHostCandidate_AddressReplaced verifies that one host candidate line has its IP replaced while other tokens are preserved.
func TestRewriteHostCandidates_SingleHostCandidate_AddressReplaced(t *testing.T) {
	sdp := `v=0
o=- 123 456 IN IP4 192.168.1.1
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host generation 0`

	result := rewriteHostCandidates(sdp, "127.0.0.1")

	if !strings.Contains(result, "127.0.0.1 40000 typ host") {
		t.Errorf("expected candidate with 127.0.0.1, got %q", result)
	}
	if !strings.Contains(result, "a=candidate:1 1 udp 2130706431") {
		t.Errorf("expected candidate prefix unchanged, got %q", result)
	}
	if !strings.Contains(result, "generation 0") {
		t.Errorf("expected candidate suffix unchanged, got %q", result)
	}
}

// TestRewriteHostCandidates_ConnectionLine_AddressReplaced verifies that c=IN IP4 line has its address replaced.
func TestRewriteHostCandidates_ConnectionLine_AddressReplaced(t *testing.T) {
	sdp := `v=0
c=IN IP4 192.168.1.1
o=- 123 456 IN IP4 192.168.1.1`

	result := rewriteHostCandidates(sdp, "10.0.0.1")

	if !strings.Contains(result, "c=IN IP4 10.0.0.1") {
		t.Errorf("expected connection line with 10.0.0.1, got %q", result)
	}
	if strings.Contains(result, "c=IN IP4 192.168.1.1") {
		t.Errorf("expected old connection IP replaced, got %q", result)
	}
}

// TestRewriteHostCandidates_BothCandidateAndConnectionLine_BothReplaced verifies that both candidate and connection lines are replaced independently.
func TestRewriteHostCandidates_BothCandidateAndConnectionLine_BothReplaced(t *testing.T) {
	sdp := `v=0
c=IN IP4 192.168.1.1
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host generation 0`

	result := rewriteHostCandidates(sdp, "10.20.30.40")

	if !strings.Contains(result, "c=IN IP4 10.20.30.40") {
		t.Errorf("expected connection line rewritten, got %q", result)
	}
	if !strings.Contains(result, "10.20.30.40 40000 typ host") {
		t.Errorf("expected candidate line rewritten, got %q", result)
	}
	if strings.Contains(result, "192.168.1.1") {
		t.Errorf("expected all instances of 192.168.1.1 replaced, got %q", result)
	}
}

// TestRewriteHostCandidates_MultipleCandidateLines_AllReplaced verifies that all 3 distinct host candidate lines are rewritten.
func TestRewriteHostCandidates_MultipleCandidateLines_AllReplaced(t *testing.T) {
	sdp := `v=0
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host generation 0
a=candidate:2 1 udp 2130706431 192.168.1.1 40001 typ host generation 0
a=candidate:3 1 udp 2130706431 192.168.1.1 40002 typ host generation 0`

	result := rewriteHostCandidates(sdp, "172.16.0.1")

	// Count occurrences of the rewritten IP in candidate lines
	hostCandidateCount := strings.Count(result, "172.16.0.1 40")
	if hostCandidateCount < 3 {
		t.Errorf("expected at least 3 rewritten host candidates, got %d in %q", hostCandidateCount, result)
	}
	if strings.Contains(result, "192.168.1.1 40") {
		t.Errorf("expected old IPs in candidate lines replaced, got %q", result)
	}
}

// TestRewriteHostCandidates_SrflxCandidateLine_Untouched verifies that srflx candidate lines are NOT modified.
func TestRewriteHostCandidates_SrflxCandidateLine_Untouched(t *testing.T) {
	sdp := `v=0
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host generation 0
a=candidate:2 1 udp 1686052607 203.0.113.1 54321 typ srflx raddr 192.168.1.1 rport 40000`

	result := rewriteHostCandidates(sdp, "10.0.0.1")

	if !strings.Contains(result, "10.0.0.1 40000 typ host") {
		t.Errorf("expected host candidate rewritten, got %q", result)
	}
	// srflx candidate should remain untouched
	if !strings.Contains(result, "203.0.113.1 54321 typ srflx") {
		t.Errorf("expected srflx candidate untouched, got %q", result)
	}
}

// TestRewriteHostCandidates_NoCandidateLines_OnlyConnectionLine verifies that only c= line is rewritten when no candidate lines exist.
func TestRewriteHostCandidates_NoCandidateLines_OnlyConnectionLine(t *testing.T) {
	sdp := `v=0
o=- 123 456 IN IP4 192.168.1.1
c=IN IP4 192.168.1.1
s=test session`

	result := rewriteHostCandidates(sdp, "10.0.0.1")

	if !strings.Contains(result, "c=IN IP4 10.0.0.1") {
		t.Errorf("expected connection line rewritten, got %q", result)
	}
	// Should not have any other rewritten lines
	lines := strings.Split(result, "\n")
	rewriteCount := 0
	for _, line := range lines {
		if strings.Contains(line, "10.0.0.1") {
			rewriteCount++
		}
	}
	if rewriteCount != 1 {
		t.Errorf("expected exactly 1 rewritten line, got %d in %q", rewriteCount, result)
	}
}

// TestRewriteHostCandidates_MalformedCandidateLine_Untouched verifies that malformed candidate lines pass through unchanged without panicking.
func TestRewriteHostCandidates_MalformedCandidateLine_Untouched(t *testing.T) {
	sdp := `v=0
a=candidate:incomplete line missing fields
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host
a=notacandidate:line`

	result := rewriteHostCandidates(sdp, "10.0.0.1")

	if !strings.Contains(result, "10.0.0.1 40000 typ host") {
		t.Errorf("expected valid candidate rewritten, got %q", result)
	}
	if !strings.Contains(result, "a=candidate:incomplete line missing fields") {
		t.Errorf("expected malformed line untouched, got %q", result)
	}
	if !strings.Contains(result, "a=notacandidate:line") {
		t.Errorf("expected non-candidate line untouched, got %q", result)
	}
}

// TestRewriteHostCandidates_IPv6AdvertisedIP verifies that IPv6 advertisedIP does not panic and address field contains it.
func TestRewriteHostCandidates_IPv6AdvertisedIP(t *testing.T) {
	sdp := `v=0
c=IN IP4 192.168.1.1
a=candidate:1 1 udp 2130706431 192.168.1.1 40000 typ host`

	// Should not panic with IPv6 address (even though it's mixing IP versions)
	result := rewriteHostCandidates(sdp, "::1")

	if !strings.Contains(result, "::1") {
		t.Errorf("expected IPv6 address in result, got %q", result)
	}
}

// TestRewriteHostCandidates_EmptySDP_EmptyResult verifies that empty SDP string returns empty string with non-empty advertisedIP.
func TestRewriteHostCandidates_EmptySDP_EmptyResult(t *testing.T) {
	result := rewriteHostCandidates("", "127.0.0.1")

	if result != "" {
		t.Errorf("expected empty result for empty SDP, got %q", result)
	}
}

// === Tier 2: Manager.CreateSession Integration Tests ===

// newTestClientOffer creates a test pion PeerConnection that generates an offer with a recvonly video
// transceiver and a pre-negotiated data channel (id=1, label="input-events"). It uses a separate
// UDP port range from the Manager under test to avoid binding collisions. Returns the offer SDP
// string, the client PeerConnection, and the client's data channel reference.
func newTestClientOffer(t *testing.T) (offerSDP string, clientPC *pion.PeerConnection, clientDC *pion.DataChannel) {
	t.Helper()

	// Use a separate port range for the test client (offset from Manager's default 40000-40009)
	var se pion.SettingEngine
	if portErr := se.SetEphemeralUDPPortRange(40100, 40109); portErr != nil {
		t.Fatalf("newTestClientOffer: set UDP port range: %v", portErr)
	}

	// Same MediaEngine requirement as the production Manager: without
	// registered codecs, offer/answer negotiation for the video m-line can
	// never converge.
	mediaEngine := &pion.MediaEngine{}
	if mediaErr := mediaEngine.RegisterDefaultCodecs(); mediaErr != nil {
		t.Fatalf("newTestClientOffer: register default codecs: %v", mediaErr)
	}
	api := pion.NewAPI(pion.WithSettingEngine(se), pion.WithMediaEngine(mediaEngine))

	pc, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("newTestClientOffer: new peer connection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	// Add a recvonly video transceiver (browser receives VP8 from server)
	_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{
		Direction: pion.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		t.Fatalf("newTestClientOffer: add video transceiver: %v", err)
	}

	// Create pre-negotiated data channel (must match server-side id=1 and label="input-events")
	negotiated := true
	channelID := uint16(1)
	dc, err := pc.CreateDataChannel(inputChannelLabel, &pion.DataChannelInit{
		Negotiated: &negotiated,
		ID:         &channelID,
	})
	if err != nil {
		t.Fatalf("newTestClientOffer: create data channel: %v", err)
	}

	// Create offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("newTestClientOffer: create offer: %v", err)
	}

	// Wait for ICE gathering to complete before returning
	gatherComplete := pion.GatheringCompletePromise(pc)

	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("newTestClientOffer: set local description: %v", err)
	}

	select {
	case <-gatherComplete:
		// ICE gathering complete, proceed
	case <-time.After(10 * time.Second):
		t.Fatalf("newTestClientOffer: ICE gathering timeout")
	}

	localDesc := pc.LocalDescription()
	if localDesc == nil {
		t.Fatalf("newTestClientOffer: local description is nil after gathering")
	}

	return localDesc.SDP, pc, dc
}

// TestManager_CreateSession_ReturnedAnswerSDP_NonEmpty verifies that CreateSession returns a non-empty answer SDP containing "v=0".
func TestManager_CreateSession_ReturnedAnswerSDP_NonEmpty(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	if answerSDP == "" {
		t.Error("expected non-empty answer SDP")
	}
	if !strings.Contains(answerSDP, "v=0") {
		t.Errorf("expected 'v=0' in answer SDP, got %q", answerSDP)
	}
}

// TestManager_CreateSession_ReturnedAnswerSDP_Parseable verifies that the client can call SetRemoteDescription with the returned answer SDP without error.
func TestManager_CreateSession_ReturnedAnswerSDP_Parseable(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	// Client should be able to set the remote description (server's answer)
	answer := pion.SessionDescription{
		Type: pion.SDPTypeAnswer,
		SDP:  answerSDP,
	}
	if err := clientPC.SetRemoteDescription(answer); err != nil {
		t.Fatalf("SetRemoteDescription failed: %v", err)
	}
}

// TestManager_CreateSession_AnswerContainsVideoSection verifies that the answer SDP has an m=video section with sendonly direction.
func TestManager_CreateSession_AnswerContainsVideoSection(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	if !strings.Contains(answerSDP, "m=video") {
		t.Errorf("expected m=video in answer, got %q", answerSDP)
	}
	if !strings.Contains(answerSDP, "a=sendonly") {
		t.Errorf("expected a=sendonly in answer, got %q", answerSDP)
	}
}

// TestManager_CreateSession_DataChannelID1_Negotiated verifies that after mutual SetRemoteDescription, the client's data channel fires OnOpen.
func TestManager_CreateSession_DataChannelID1_Negotiated(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, clientDC := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	// Set the answer on the client side
	answer := pion.SessionDescription{
		Type: pion.SDPTypeAnswer,
		SDP:  answerSDP,
	}
	if err := clientPC.SetRemoteDescription(answer); err != nil {
		t.Fatalf("SetRemoteDescription failed: %v", err)
	}

	// Wait for the client's data channel to open. Register the callback before the channel
	// opens to avoid missing the transition. If it's already open, the test should pass.
	dataChannelOpenCh := make(chan struct{})
	clientDC.OnOpen(func() {
		close(dataChannelOpenCh)
	})

	// Wait for data channel to open (or timeout if it takes too long)
	select {
	case <-dataChannelOpenCh:
		// Success - channel opened and callback fired
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for data channel OnOpen")
	}
}

// TestManager_CreateSession_ICEConnectionReachesConnected verifies that server-side Session.PC reaches connected state.
func TestManager_CreateSession_ICEConnectionReachesConnected(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess == nil {
		t.Fatalf("expected non-nil session")
	}
	t.Cleanup(func() { _ = sess.PC.Close() })

	// Set the answer on the client side
	answer := pion.SessionDescription{
		Type: pion.SDPTypeAnswer,
		SDP:  answerSDP,
	}
	if err := clientPC.SetRemoteDescription(answer); err != nil {
		t.Fatalf("SetRemoteDescription failed: %v", err)
	}

	// Wait for the server-side PC to reach connected state
	connectedCh := make(chan struct{})
	sess.PC.OnConnectionStateChange(func(s pion.PeerConnectionState) {
		if s == pion.PeerConnectionStateConnected {
			close(connectedCh)
		}
	})

	select {
	case <-connectedCh:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for server PC to reach connected state")
	}
}

// TestManager_CreateSession_SessionFields_NotNil verifies that the returned Session has non-nil PC, VideoTrack, and InputChannel.
func TestManager_CreateSession_SessionFields_NotNil(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.PC == nil {
		t.Error("expected non-nil Session.PC")
	}
	if sess.VideoTrack == nil {
		t.Error("expected non-nil Session.VideoTrack")
	}
	if sess.InputChannel == nil {
		t.Error("expected non-nil Session.InputChannel")
	}
}

// TestManager_CreateSession_AdvertisedIPRewrittenInAnswer verifies that with AdvertisedIp="127.0.0.1", the answer SDP contains it in both c= and candidate lines.
func TestManager_CreateSession_AdvertisedIPRewrittenInAnswer(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "127.0.0.1",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	if !strings.Contains(answerSDP, "c=IN IP4 127.0.0.1") {
		t.Errorf("expected c=IN IP4 127.0.0.1 in answer, got %q", answerSDP)
	}
	if !strings.Contains(answerSDP, "127.0.0.1 40") || !strings.Contains(answerSDP, "typ host") {
		t.Errorf("expected rewritten host candidate in answer, got %q", answerSDP)
	}
}

// TestManager_CreateSession_EmptyAdvertisedIP_CandidatesPresent verifies that with AdvertisedIp="", CreateSession succeeds and returns a non-empty answer.
func TestManager_CreateSession_EmptyAdvertisedIP_CandidatesPresent(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess != nil {
		t.Cleanup(func() { _ = sess.PC.Close() })
	}

	if answerSDP == "" {
		t.Error("expected non-empty answer SDP with empty AdvertisedIp")
	}
	// Should still have candidates (not rewritten, but present)
	if !strings.Contains(answerSDP, "a=candidate:") {
		t.Errorf("expected candidates in answer, got %q", answerSDP)
	}
}

// TestManager_CreateSession_ContextCancelled_ReturnsError verifies that a cancelled context returns an error and nil session.
func TestManager_CreateSession_ContextCancelled_ReturnsError(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40000,
		UdpPortEnd:   40009,
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	// Use an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)

	if err == nil {
		t.Error("expected error with cancelled context")
	}
	if answerSDP != "" {
		t.Errorf("expected empty answerSDP with error, got %q", answerSDP)
	}
	if sess != nil {
		t.Error("expected nil session with error")
	}
}

// TestManager_CreateSession_InvalidPortRange_ReturnsError verifies that an invalid port range (Start > End) returns an error promptly.
func TestManager_CreateSession_InvalidPortRange_ReturnsError(t *testing.T) {
	cfg := &config.WebRtcConfig{
		AdvertisedIp: "",
		UdpPortStart: 40009,
		UdpPortEnd:   40000, // Invalid: start > end
	}
	m := NewManager(cfg)

	offerSDP, clientPC, _ := newTestClientOffer(t)
	t.Cleanup(func() { _ = clientPC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answerSDP, sess, err := m.CreateSession(ctx, offerSDP)

	if err == nil {
		t.Error("expected error with invalid port range")
	}
	if answerSDP != "" {
		t.Errorf("expected empty answerSDP with error, got %q", answerSDP)
	}
	if sess != nil {
		t.Error("expected nil session with error")
	}
}
