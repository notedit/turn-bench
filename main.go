package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v2"
)

func GenRandomBytes(size int) (blk []byte) {
	blk = make([]byte, size)
	rand.Read(blk)
	return
}

func main() {
	host := flag.String("host", "", "TURN Server name.")
	port := flag.Int("port", 3478, "Listening port.")
	user := flag.String("user", "", "user")
	password := flag.String("password", "", "password")
	realm := flag.String("realm", "trtc.one", "trtc.one")
	clients := flag.Int("clients", 1, "how many client to start")
	packets := flag.Int("packets", 10, "how many packets to send per client per millisecond")
	duration := flag.Int("duration", 60, "how many seconds to bench")

	flag.Parse()

	var wg sync.WaitGroup

	if len(*host) == 0 {
		log.Fatalf("'host' is required")
	}

	if len(*user) == 0 {
		log.Fatalf("'user' is required")
	}

	if len(*password) == 0 {
		log.Fatalf("'password' is required")
	}

	log.Printf("Run clients %d  duration %d   %d  packets per client per millisecond\n", *clients, *duration, *packets)

	for i := 0; i < *clients; i++ {

		go func(i int) {
			// TURN client won't create a local listening socket by itself.
			conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
			if err != nil {
				panic(err)
			}
			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					panic(closeErr)
				}
			}()

			turnServerAddr := fmt.Sprintf("%s:%d", *host, *port)

			cfg := &turn.ClientConfig{
				STUNServerAddr: turnServerAddr,
				TURNServerAddr: turnServerAddr,
				Conn:           conn,
				Username:       *user,
				Password:       *password,
				Realm:          *realm,
				LoggerFactory:  logging.NewDefaultLoggerFactory(),
			}

			client, err := turn.NewClient(cfg)
			if err != nil {
				panic(err)
			}
			defer client.Close()

			// Start listening on the conn provided.
			err = client.Listen()
			if err != nil {
				panic(err)
			}

			// Allocate a relay socket on the TURN server. On success, it
			// will return a net.PacketConn which represents the remote
			// socket.
			relayConn, err := client.Allocate()

			if err != nil {
				panic(err)
			}
			defer func() {
				if closeErr := relayConn.Close(); closeErr != nil {
					panic(closeErr)
				}
			}()

			// The relayConn's local address is actually the transport
			// address assigned on the TURN server.
			log.Printf("relayed-address=%s", relayConn.LocalAddr().String())

			wg.Add(1)
			err = doPingTest(client, relayConn, *packets)
			wg.Done()
			if err != nil {
				panic(err)
			}
		}(i)
	}

	wg.Wait()
	log.Println("Bench Done")
}

func doPingTest(client *turn.Client, relayConn net.PacketConn, packets int) error {

	// Send BindingRequest to learn our external IP
	mappedAddr, err := client.SendBindingRequest()
	if err != nil {
		return err
	}

	// Set up pinger socket (pingerConn)
	pingerConn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := pingerConn.Close(); closeErr != nil {
			panic(closeErr)
		}
	}()

	// Punch a UDP hole for the relayConn by sending a data to the mappedAddr.
	// This will trigger a TURN client to generate a permission request to the
	// TURN server. After this, packets from the IP address will be accepted by
	// the TURN server.
	_, _ = relayConn.WriteTo([]byte("Hello"), mappedAddr)

	// send twice just in case packet drop
	_, err = relayConn.WriteTo([]byte("Hello"), mappedAddr)
	if err != nil {
		return err
	}

	// Start read-loop on pingerConn
	go func() {
		buf := make([]byte, 1600)
		for {
			n, from, pingerErr := pingerConn.ReadFrom(buf)
			if pingerErr != nil {
				break
			}

			log.Printf("%d bytes from from %s\n", n, from.String())

		}
	}()

	// Start read-loop on relayConn
	go func() {
		buf := make([]byte, 1600)
		for {
			n, from, readerErr := relayConn.ReadFrom(buf)
			if readerErr != nil {
				break
			}

			// Echo back
			if _, readerErr = relayConn.WriteTo(buf[:n], from); readerErr != nil {
				break
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)

	now := time.Now()

	msg := GenRandomBytes(1000)
	for {
		for i := 0; i < packets; i++ {
			_, err = pingerConn.WriteTo(msg, relayConn.LocalAddr())
			if err != nil {
				return err
			}
		}
		time.Sleep(time.Millisecond)
		if time.Since(now) > time.Minute*10 {
			break
		}
	}

	return nil
}
