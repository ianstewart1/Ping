package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

func main() {

	iters := flag.Int("n", -1, "Number of echo requests to send. -1 = infinite.")
	timeout := flag.Int("w", 5000, "Timeout in milliseconds to wait for each reply.")
	flag.Parse()
	ip := flag.Arg(0)
	if ip == "" {
		fmt.Println("Usage: goping [Arguments] IP/Hostname")
		flag.PrintDefaults()
		return
	}
	closeHandler()

	parsedIP := parseIP(ip)

	pl := packetLoss{
		totalPackets: 0,
		lostPackets:  0,
	}

	if *iters < 0 {
		fmt.Printf("Pinging %s [%s]\n", ip, parsedIP.String())
		for {
			time.Sleep(time.Second)
			ping(parsedIP, *timeout, &pl)
		}
	} else {
		fmt.Printf("Pinging %s [%s] %v times\n", ip, parsedIP.String(), *iters)
		for i := 0; i < *iters; i++ {
			time.Sleep(time.Second)
			ping(parsedIP, *timeout, &pl)
		}
	}

}

func parseIP(ip string) *net.IPAddr {
	addr := ip
	if strings.Contains(ip, "www.") {
		addr = strings.Split(ip, "www.")[1]
	}

	dest, err := net.ResolveIPAddr("ip4", addr)
	if err != nil {
		log.Fatal(err)
	}
	return dest
}

func ping(addr *net.IPAddr, timeout int, pl *packetLoss) {
	pl.totalPackets++
	channel, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0") // Listening address defaults to 0.0.0.0
	if err != nil {
		log.Fatal(err)
	}
	defer channel.Close()

	message := createICMPMessage()
	rp := sendPing(channel, addr, message, timeout, pl)
	if rp.lenMsg == -1 {
		printTimeout(addr, timeout)
		return
	}

	replyMsg, err := icmp.ParseMessage(1, rp.msg) // ProtocalICMP = 1 from iana
	if err != nil {
		log.Fatal(err)
	}

	printPingReply(replyMsg, rp.dur, rp.addr, pl)
}

func createICMPMessage() []byte {
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{
			ID: os.Getpid() & 0xffff, Seq: 1,
			Data: []byte(""),
		},
	}
	encoded, err := msg.Marshal(nil) // Param only set for IPv6 checksums (must be pseudo header if used)
	if err != nil {
		log.Fatal(err)
	}
	return encoded
}

func sendPing(ch *icmp.PacketConn, dest *net.IPAddr, msg []byte, timeout int, pl *packetLoss) readPacket {
	sendTime := time.Now()

	_, err := ch.WriteTo(msg, dest)
	if err != nil {
		log.Fatal(err)
	}

	replyBytes := make([]byte, 1500) // Defaulted to 1500 per Golang imcp docs

	c := make(chan readPacket)
	var r readPacket
	go func() {
		n, replyIP, err := ch.ReadFrom(replyBytes)
		c <- readPacket{
			lenMsg: n,
			addr:   replyIP,
			err:    err,
		}
	}()

	select {
	// If reply is within timeout
	case res := <-c:
		r = res
	// If reply takes longer than timeout
	case <-time.After(time.Duration(timeout * 1000000)): // time.Duration stores time in nanoseconds (int64)
		pl.lostPackets++
		return readPacket{
			lenMsg: -1,
		}
	}

	if err != nil {
		log.Fatal(err)
	}

	r.dur = time.Since(sendTime)
	r.msg = replyBytes[:r.lenMsg]
	return r
}

func printTimeout(addr net.Addr, timeout int) {
	fmt.Println("Request timed out.")
}

func printPingReply(rm *icmp.Message, dur time.Duration, raddr net.Addr, pl *packetLoss) {
	loss := int(math.Ceil(float64(pl.lostPackets/pl.totalPackets) * 100))
	if rm.Type != ipv4.ICMPTypeEchoReply {
		fmt.Printf("Expecting echo reply, instead got code %v\n", rm.Type)
	}
	switch rm.Code {
	case 0:
		fmt.Printf("Reply from %v: time=%s loss=%v%%\n", raddr.String(), dur.Truncate(time.Millisecond), loss)
	case 1:
		fmt.Println("Could not reach host")
	default:
		fmt.Printf("Received code %v\n", rm.Code)
	}
}

type packetLoss struct {
	totalPackets, lostPackets int
}

type readPacket struct {
	lenMsg int
	addr   net.Addr
	msg    []byte
	dur    time.Duration
	err    error
}

func closeHandler() {
	// Catch ctrl+c and exit smoothly
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("Done.")
		os.Exit(0)
	}()
}
