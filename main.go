package main

import (
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/imyousuf/lan-messenger/network"
)

type _MessageListener struct {
	completeNotificationChannel chan int
}

func (me _MessageListener) HandleMessageReceived(event network.MessageEvent) {
	log.Println(event.GetMessage())
}
func (me _MessageListener) HandleEndOfMessages() {
	me.completeNotificationChannel <- 1
}

type _BroadcastListener struct {
	completeNotificationChannel chan int
}

func (be _BroadcastListener) HandleBroadcastReceived(event network.BroadcastEvent) {
	log.Println(event.GetBroadcastMessage())
}
func (be _BroadcastListener) HandleEndOfBroadcasts() {
	be.completeNotificationChannel <- 2
}

func exit(udpComm network.Communication) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		udpComm.CloseCommunication()
	}()
}

func main() {
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, netInterface := range interfaces {
			// Unicast addresses
			network.PrintIPv4Addresses(netInterface, true)
			// Multicast addresses
			network.PrintIPv4Addresses(netInterface, false)
		}
	} else {
		log.Fatal("Error getting interfaces", err)
	}
	completeNotificationChannel := make(chan int)
	messageListener := _MessageListener{completeNotificationChannel: completeNotificationChannel}
	broadcastListener := _BroadcastListener{completeNotificationChannel: completeNotificationChannel}
	udpComm := network.NewUDPCommunication()
	config := network.NewConfig(30000)
	exit(udpComm)
	udpComm.AddMessageListener(&messageListener)
	udpComm.AddBroadcastListener(&broadcastListener)
	udpComm.SetupCommunication(config)
	<-completeNotificationChannel
	<-completeNotificationChannel
}
