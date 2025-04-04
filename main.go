// package main

// import (
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"log"
// 	"net/http"
// 	"sync"
// 	"time"

// 	//"github.com/pion/rtp"
// 	"github.com/pion/webrtc/v3"
// )

// type Peer struct {
// 	PC     *webrtc.PeerConnection
// 	Tracks map[string]*webrtc.TrackLocalStaticRTP // e.g., video/audio
// }

// var peers sync.Map // key: *webrtc.PeerConnection, value: *Peer

// func decodeSDP(r *http.Request, desc *webrtc.SessionDescription) error {
// 	body, err := io.ReadAll(r.Body)
// 	if err != nil {
// 		return err
// 	}
// 	return json.Unmarshal(body, desc)
// }

// func encodeSDP(w http.ResponseWriter, desc webrtc.SessionDescription) {
// 	w.Header().Set("Content-Type", "application/json")
// 	json.NewEncoder(w).Encode(desc)
// }

// func handleOffer(w http.ResponseWriter, r *http.Request) {
// 	var offer webrtc.SessionDescription
// 	if err := decodeSDP(r, &offer); err != nil {
// 		http.Error(w, err.Error(), http.StatusBadRequest)
// 		return
// 	}

// 	config := webrtc.Configuration{
// 		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
// 	}

// 	pc, err := webrtc.NewPeerConnection(config)
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}

// 	peer := &Peer{PC: pc, Tracks: make(map[string]*webrtc.TrackLocalStaticRTP)}
// 	peers.Store(pc, peer)

// 	var remoteAddr string

// 	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {

// 	// First, get the selected pair ID from the transport stat
// 		log.Printf("ICE state: %s", state)
// 		if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
// 			time.Sleep(300 * time.Millisecond)
// 			stats := pc.GetStats()
// 			var selectedCandidatePairID string
// 			for _, stat := range stats {
// 				if transport, ok := stat.(webrtc.TransportStats); ok {
// 					selectedCandidatePairID = transport.SelectedCandidatePairID
// 					break
// 				}
// 			}
// 			for _, stat := range stats {
// 				if pair, ok := stat.(webrtc.ICECandidatePairStats); ok {
// 					if pair.ID == selectedCandidatePairID {
// 						for _, s := range stats {
// 							if remote, ok := s.(webrtc.ICECandidateStats); ok && remote.ID == pair.RemoteCandidateID {
// 								remoteAddr = fmt.Sprintf("%s:%d", remote.IP, remote.Port)
// 								log.Printf("Connected peer IP: %s", remoteAddr)
// 								break
// 							}
// 						}
// 					}
// 				}
// 			}
// 		}
// 	})

// 	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
// 		log.Printf("Received track: kind=%s, codec=%s", track.Kind(), track.Codec().MimeType)

// 		outTrack, err := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
// 		if err != nil {
// 			log.Printf("Failed to create outbound track: %v", err)
// 			return
// 		}

// 		peer.Tracks[track.Kind().String()] = outTrack

// 		// Add this track to all other peers
// 		peers.Range(func(_, v interface{}) bool {
// 			other := v.(*Peer)
// 			if other.PC == pc {
// 				return true
// 			}

// 			sender, err := other.PC.AddTrack(outTrack)
// 			if err != nil {
// 				log.Printf("Error adding track to peer: %v", err)
// 				return true
// 			}

// 			go func() {
// 				rtcpBuf := make([]byte, 1500)
// 				for {
// 					if _, _, err := sender.Read(rtcpBuf); err != nil {
// 						return
// 					}
// 				}
// 			}()
// 			return true
// 		})

// 		// Forward packets
// 		go func() {
// 			buf := make([]byte, 1500)
// 			for {
// 				n, _, err := track.Read(buf)
// 				if err != nil {
// 					log.Printf("Error reading RTP: %v", err)
// 					return
// 				}

// 				peers.Range(func(_, v interface{}) bool {
// 					other := v.(*Peer)
// 					if other.PC == pc {
// 						return true
// 					}
// 					if t, ok := other.Tracks[track.Kind().String()]; ok {
// 						if _, err := t.Write(buf[:n]); err != nil {
// 							log.Printf("Error forwarding RTP: %v", err)
// 						} else {
// 							log.Printf("Forwarding RTP to")
// 						}
// 					}
// 					return true
// 				})
// 			}
// 		}()
// 	})

// 	if err := pc.SetRemoteDescription(offer); err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}

// 	answer, err := pc.CreateAnswer(nil)
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
// 	if err := pc.SetLocalDescription(answer); err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}

// 	encodeSDP(w, *pc.LocalDescription())
// }

// func main() {
// 	http.HandleFunc("/offer", handleOffer)
// 	log.Println("SFU Server running on :8080")
// 	log.Fatal(http.ListenAndServe(":8080", nil))
// }

package main

import (
    "encoding/json"
    "fmt"
    "log"
    "math/rand"
    "net/http"
    "sync"
    "time"

    "github.com/pion/webrtc/v3"
)

type Peer struct {
    ID               string
    PC               *webrtc.PeerConnection
    OutTracks        map[string]*webrtc.TrackLocalStaticRTP
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
        OfferChan:        make(chan webrtc.SessionDescription, 1),
        RemoteAnswerChan: make(chan webrtc.SessionDescription, 1),
    }

    peers.Store(peerID, peer)

    pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
        log.Printf("[%s] ICE state: %s", peerID, state.String())
    })

    pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
        log.Printf("[%s] Received track: %s", peerID, track.Kind().String())
        go forwardTrackToPeers(peerID, track)
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
            localTrack, ok := peer.OutTracks[track.Kind().String()]
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

func main() {
    rand.Seed(time.Now().UnixNano())
    http.HandleFunc("/offer", offerHandler)
    http.HandleFunc("/renegotiate/", renegotiateHandler)
    http.HandleFunc("/answer/", answerHandler)

    log.Println("✅ SFU Server running on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
