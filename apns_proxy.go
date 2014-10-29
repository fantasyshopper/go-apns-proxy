//  APNS Proxy Server
//
//  In case it's too slow to send them directly from your app backend.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	//Add some APNS lib here.
	"github.com/Mistobaan/go-apns"
)

//An APNS message lib will probably provide this struct,
//or you may need to add fields.
type ApnsMessage struct {
	DeviceId string `json:"device"`  //this will be the string version of the device Id
	Payload  string `json:"payload"` //the payload (which is also JSON encoded, but we want that string here)
	Lifetime int    `json:"expiry"`  //the lifetime of the message
}

//String method allows us to Printf("%s", msg)
func (a *ApnsMessage) String() string {
	return fmt.Sprintf("DeviceId=%s, Lifetime=%d, Payload=%s", a.DeviceId, a.Lifetime, a.Payload)
}

//you probably want to use the "flags" package to make these configurable
var (
	BUFFER = flag.Int("buffer", 10000, "Max number of messages to queue before blocking")
	LISTEN = flag.String("listen", "localhost:8765", "The port to listen for messages")
	//APNS stuff
	APNS_CERT  = flag.String("apns:cert", "apns.cert.pem", "path to APNS certificate")
	APNS_KEY   = flag.String("apns:key", "apns.key.pem", "path to APNS private key")
	SBOX_CERT  = flag.String("sbox:cert", "sbox.cert.pem", "path to APNS Sandbox certificate")
	SBOX_KEY   = flag.String("sbox:key", "sbox.key.pem", "path to APNS Sandbox key")
	BackupPath = flag.String("backup_path", ".back_apns_proxy", "save backup file path")
	Backup     = flag.Bool("backup", false, "send notification of backup file")

	closeApnsChannel   = false
	closeTCPConnection = false
)

//Constants
const (
	APNS_ENDPOINT = "gateway.push.apple.com:2195"
	SBOX_ENDPOINT = "gateway.sandbox.push.apple.com:2195"
)

//This is the programs entry point/
func main() {
	//Parse command line flags.
	flag.Parse()

	//create APNS Connections.
	ProdAPNS, err := apns.NewClient(APNS_ENDPOINT, *APNS_CERT, *APNS_KEY)
	if err != nil {
		log.Fatalf("Live APNS Gateway fail: %s\n", err)
	}
	SandBoxAPNS, err := apns.NewClient(SBOX_ENDPOINT, *SBOX_CERT, *SBOX_KEY)
	if err != nil {
		log.Fatalf("Sandbox APNS Gateway fail: %s\n", err)
	}

	// Do backup
	if *Backup {
		b, err := loadBackup()
		if err != nil {
			log.Fatal(err)
		}
		var apns []ApnsMessage
		err = json.Unmarshal(b, &apns)
		if err != nil {
			log.Fatal(err)
		}

		for _, msg := range apns {
			log.Printf("Sending APNS msg: %s\n", msg)
			if err := ProdAPNS.SendPayloadString(msg.DeviceId, []byte(msg.Payload), time.Duration(msg.Lifetime)*time.Second); err != nil {
				log.Printf("ERROR on Main Gateway: %s\n", err)
				if err := SandBoxAPNS.SendPayloadString(msg.DeviceId, []byte(msg.Payload), time.Duration(msg.Lifetime)*time.Second); err != nil {
					log.Printf("ERROR on Sandbox Gateway: %s\n", err)
				} else {
					log.Printf("SENT Sandbox Gateway: %s\n", msg.DeviceId)
				}
			} else {
				log.Printf("SENT Main Gateway: %s\n", msg.DeviceId)
			}
		}
		os.Rename(*BackupPath, fmt.Sprintf("%s.%s", *BackupPath, time.Now().Format("20060102150405")))
		return
	}

	// detect signal of interrupt, kill
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, os.Interrupt, os.Kill)

	apnsMessage := startInboundListener()

	for {
		select {
		case msg := <-apnsMessage:
			log.Printf("Sending APNS msg: %s\n", msg)
			if err := ProdAPNS.SendPayloadString(msg.DeviceId, []byte(msg.Payload), time.Duration(msg.Lifetime)*time.Second); err != nil {
				log.Printf("ERROR on Main Gateway: %s\n", err)
				if err := SandBoxAPNS.SendPayloadString(msg.DeviceId, []byte(msg.Payload), time.Duration(msg.Lifetime)*time.Second); err != nil {
					log.Printf("ERROR on Sandbox Gateway: %s\n", err)
				} else {
					log.Printf("SENT Sandbox Gateway: %s\n", msg.DeviceId)
				}
			} else {
				log.Printf("SENT Main Gateway: %s\n", msg.DeviceId)
			}
		case <-done:
			log.Println("detect signal. start cleanup...")
			f, err := os.Create(*BackupPath)
			if err != nil {
				log.Fatal(err)
			}
			cleanup(apnsMessage, f)
			log.Println("done cleanup! please input '-backup flag' on next time")
			os.Exit(0)
		}
	}
}

func loadBackup() (b []byte, err error) {
	b, err = ioutil.ReadFile(*BackupPath)
	return
}

func cleanup(mch chan *ApnsMessage, out io.Writer) (n int, err error) {
	closeTCPConnection = true

	// timeout apnsMessage
	go func() {
		time.Sleep(time.Second * 30)
		closeApnsChannel = true
		close(mch)
	}()

	var bufferMsg []ApnsMessage

	for {
		m, ok := <-mch
		if !ok || m == nil {
			break
		}
		bufferMsg = append(bufferMsg, *m)
	}

	if len(bufferMsg) == 0 {
		return
	}

	b, err := json.Marshal(bufferMsg)
	if err != nil {
		return
	}
	n, err = out.Write(b)
	if err != nil {
		return
	}

	return
}

//This returns a channel new messages come in on.
func startInboundListener() chan *ApnsMessage {
	//Make the channel, buffered by BUFFER
	ch := make(chan *ApnsMessage, *BUFFER)
	//bind to the host/port
	ln, err := net.Listen("tcp", *LISTEN)
	if err != nil {
		//in the event of connection error, die.
		log.Fatalf("TCP Listen: %s\n", err)
	}
	log.Printf("Listening on %s\n", *LISTEN)
	//start a go routine that accepts connections.
	go func() {
		for {
			defer ln.Close()
			if closeTCPConnection {
				return
			}

			//in an infinite loop
			conn, err := ln.Accept() //accept new conenctions
			if err != nil {
				log.Printf("ERROR: %s\n", err)
				continue
			}
			go func(c net.Conn) { //spawn a handler gorountine for this connection
				defer c.Close()
				log.Print("New Connection\n")
				dec := json.NewDecoder(c) // create a JSON decoder which reads from the connection
				for {                     //in an inifinite loop
					out := &ApnsMessage{}                   //pointer to an empty message
					if err := dec.Decode(out); err != nil { //if there was an error decoding the JSON into our message
						log.Printf("JSON Decode ERROR: %s\n", err) //crap out
						return
					}
					log.Printf("Received msg: %s\n", out)
					if !closeApnsChannel {
						ch <- out //send message back on channel
					}
				}
			}(conn) //pass in the current connection handle
		}
	}()
	return ch //and return the channel to the caller
}
