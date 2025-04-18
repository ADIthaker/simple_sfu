package main

import (
    "encoding/json"
    "fmt"
    "log"
    "math/rand"
    "net/http"
    "sync"
    "time"
    "github.com/cilium/ebpf"
    "github.com/pion/webrtc/v3"
)

type Peer struct {
    ID               string
    PC               *webrtc.PeerConnection
    OutTracks        map[string]*webrtc.TrackLocalStaticRTP
    InTracks         map[string]*webrtc.TrackRemote      
    OfferChan        chan webrtc.SessionDescription
    RemoteAnswerChan chan webrtc.SessionDescription
    mu               sync.Mutex
}

var peers sync.Map

func generatePeerID() string {
    return fmt.Sprintf("peer-%d", rand.Intn(1000000))
}

func newPeerConnection() (*webrtc.PeerConnection, error) {
    config := webrtc.Configuration{
        ICEServers: []webrtc.ICEServer{
            {URLs: []string{"stun:stun.l.google.com:19302"}},
        },
    }
    return webrtc.NewPeerConnection(config)
}

func offerHandler(w http.ResponseWriter, r *http.Request) {
    peerID := generatePeerID()
    pc, err := newPeerConnection()
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    peer := &Peer{
        ID:               peerID,
        PC:               pc,
        OutTracks:        make(map[string]*webrtc.TrackLocalStaticRTP),
        InTracks:         make(map[string]*webrtc.TrackRemote),
        OfferChan:        make(chan webrtc.SessionDescription, 1),
        RemoteAnswerChan: make(chan webrtc.SessionDescription, 1),
    }

    peers.Store(peerID, peer)

    pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
        log.Printf("[%s] ICE state: %s", peerID, state.String())
    })
    log.Print("Got Track from peer", peer.OutTracks)

    // pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
    //     log.Printf("[%s] Received track: %s", peerID, track.Kind().String())
    //     time.Sleep(200 * time.Millisecond)
    //     go forwardTrackToPeers(peerID, track)
    // })
    pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
        kind := track.Kind().String()
        log.Printf("[%s] Received track: %s", peerID, kind)
    
        peer.mu.Lock()
        peer.InTracks[kind] = track
        peer.mu.Unlock()
    
        // Start reading RTP packets from this track
        go func() {
            buf := make([]byte, 1500)
            for {
                n, _, err := track.Read(buf)
                if err != nil {
                    log.Printf("[%s] RTP read error: %v", peerID, err)
                    return
                }
    
                // Forward to all other peers from InTracks
                peers.Range(func(_, val any) bool {
                    other := val.(*Peer)
                    if other.ID == peerID {
                        return true // skip sender
                    }
    
                    other.mu.Lock()
                    defer other.mu.Unlock()
    
                    outTrack := other.OutTracks[kind]
                    if outTrack == nil {
                        // Create and attach outbound track
                        newTrack, err := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
                        if err != nil {
                            log.Printf("❌ Couldn't create outbound track: %v", err)
                            return true
                        }
    
                        sender, err := other.PC.AddTrack(newTrack)
                        if err != nil {
                            log.Printf("❌ Couldn't add track to peer %s: %v", other.ID, err)
                            return true
                        }
    
                        go func() {
                            rtcpBuf := make([]byte, 1500)
                            for {
                                if _, _, err := sender.Read(rtcpBuf); err != nil {
                                    return
                                }
                            }
                        }()
    
                        other.OutTracks[kind] = newTrack
    
                        // Trigger renegotiation
                        offer, err := other.PC.CreateOffer(nil)
                        if err == nil && other.PC.SetLocalDescription(offer) == nil {
                            if desc := other.PC.LocalDescription(); desc != nil {
                                select {
                                case other.OfferChan <- *desc:
                                    log.Printf("📡 Sent renegotiation offer to %s", other.ID)
                                default:
                                    log.Printf("⚠️ OfferChan full for %s", other.ID)
                                }
                            }
                        }
                    }
    
                    // Write RTP packet
                    if other.OutTracks[kind] != nil {
                        log.Printf("Sending track from %s to %s", peerID, other.ID)
                        _, err := other.OutTracks[kind].Write(buf[:n])
                        if err != nil {
                            log.Printf("⚠️ RTP forward error to %s: %v", other.ID, err)
                        }
                    }
    
                    return true
                })
            }
        }()
    })
    

    var offer webrtc.SessionDescription
    if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
        http.Error(w, "Invalid SDP", http.StatusBadRequest)
        return
    }

    if err := pc.SetRemoteDescription(offer); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    answer, err := pc.CreateAnswer(nil)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    if err := pc.SetLocalDescription(answer); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    json.NewEncoder(w).Encode(struct {
        SDP    webrtc.SessionDescription `json:"sdp"`
        PeerID string                    `json:"peer_id"`
    }{*pc.LocalDescription(), peerID})
}

func forwardTrackToPeers(fromPeerID string, track *webrtc.TrackRemote) {
    buf := make([]byte, 1500)
    for {

        n, _, err := track.Read(buf)
        if err != nil {
            log.Printf("[%s] Error reading track: %v", fromPeerID, err)
            return
        }

        peers.Range(func(key, value any) bool {
            peer := value.(*Peer)
            if peer.ID == fromPeerID {
                return true
            }

            peer.mu.Lock()
            log.Print("KIND OF TRACK: "+track.Kind().String())
            localTrack, ok := peer.OutTracks[track.Kind().String()]
            log.Print("Tracks ", localTrack)

            if !ok {

                newTrack, err := webrtc.NewTrackLocalStaticRTP(
                    track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
                if err != nil {
                    log.Printf("Error creating local track: %v", err)
                    peer.mu.Unlock()
                    return true
                }

                sender, err := peer.PC.AddTrack(newTrack)
                if err != nil {
                    log.Printf("Error adding track to peer %s: %v", peer.ID, err)
                    peer.mu.Unlock()
                    return true
                }

                go func() {
                    rtcpBuf := make([]byte, 1500)
                    for {
                        if _, _, rtcpErr := sender.Read(rtcpBuf); rtcpErr != nil {
                            return
                        }
                    }
                }()

                peer.OutTracks[track.Kind().String()] = newTrack
                localTrack = newTrack

                offer, err := peer.PC.CreateOffer(nil)
                if err == nil {
                    peer.PC.SetLocalDescription(offer)
                    peer.OfferChan <- *peer.PC.LocalDescription()
                }
            }

            if localTrack != nil {
                _, err := localTrack.Write(buf[:n])
                if err != nil {
                    log.Printf("Error forwarding packet: %v", err)
                }
            }

            peer.mu.Unlock()
            return true
        })
    }
}

func renegotiateHandler(w http.ResponseWriter, r *http.Request) {
    peerID := r.URL.Path[len("/renegotiate/"):]
    if val, ok := peers.Load(peerID); ok {
        peer := val.(*Peer)
        select {
        case offer := <-peer.OfferChan:
            json.NewEncoder(w).Encode(offer)
        case <-time.After(2 * time.Second):
            w.WriteHeader(http.StatusNoContent)
        }
    } else {
        http.Error(w, "Peer not found", http.StatusNotFound)
    }
}

func answerHandler(w http.ResponseWriter, r *http.Request) {
    peerID := r.URL.Path[len("/answer/"):]
    if val, ok := peers.Load(peerID); ok {
        peer := val.(*Peer)

        var answer webrtc.SessionDescription
        if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
            http.Error(w, "Invalid SDP", http.StatusBadRequest)
            return
        }

        if err := peer.PC.SetRemoteDescription(answer); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        w.WriteHeader(http.StatusOK)
    } else {
        http.Error(w, "Peer not found", http.StatusNotFound)
    }
}

func initEBPFMap() {
    var err error
    var ipMap any
    ipMap, err = ebpf.LoadPinnedMap("/sys/fs/bpf/peer_ips", nil)
    if err != nil {
        log.Fatalf("❌ Failed to load pinned eBPF map: %v", err)
    }
    log.Println("✅ Loaded eBPF map from /sys/fs/bpf/peer_ips")
}

func main() {
    rand.Seed(time.Now().UnixNano())
    http.HandleFunc("/offer", offerHandler)
    http.HandleFunc("/renegotiate/", renegotiateHandler)
    http.HandleFunc("/answer/", answerHandler)

    log.Println("✅ SFU Server running on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
