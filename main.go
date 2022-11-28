package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/danielpaulus/quicktime_video_hack/screencapture"
	"go.nanomsg.org/mangos/v3"
	"go.nanomsg.org/mangos/v3/protocol/push"

	// register transports
	_ "github.com/nanomsg/mangos/transport/all"

	log "github.com/sirupsen/logrus"
)

var (
	pr                   *io.PipeReader
	pw                   *io.PipeWriter
	pushSock             mangos.Socket
	prevImg              *image.RGBA
	fileMode             bool
	screenReductionRatio float64
)

func main() {
	var udid = flag.String("udid", "", "Device UDID")
	var devicesCmd = flag.Bool("devices", false, "List devices then exit")
	var pullCmd = flag.Bool("pull", false, "Pull video")
	var pushSpec = flag.String("pushSpec", "tcp://127.0.0.1:7879", "push image to tcp address")
	var file = flag.String("file", "", "File to save h264 nalus into")
	var reductionRatio = flag.Float64("screenRatio", 0.5, "Screen reduction ratio")
	var verbose = flag.Bool("v", false, "Verbose Debugging")
	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})
	screenReductionRatio = *reductionRatio

	if *verbose {
		log.Info("Set Debug mode")
		log.SetLevel(log.DebugLevel)
	}

	if *devicesCmd {
		devices()
		return
	} else if *pullCmd {
		gopull(*pushSpec, *file, *udid)
	} else {
		flag.Usage()
	}
}
func devices() {
	deviceList, err := screencapture.FindIosDevices()
	if err != nil {
		printErrJSON(err, "Error finding iOS Devices")
	}
	log.Debugf("Found (%d) iOS Devices with UsbMux Endpoint", len(deviceList))

	if err != nil {
		printErrJSON(err, "Error finding iOS Devices")
	}
	output := screencapture.PrintDeviceDetails(deviceList)

	printJSON(map[string]interface{}{"devices": output})
}

//func stripSerial(usb *gousb.Device) string {
//      str, _ := usb.SerialNumber()
//      return stripCtlFromBytes(str)
//}

func gopull(pushSpec string, filename string, udid string) {
	stopChannel := make(chan interface{})
	stopChannel2 := make(chan interface{})
	stopChannel3 := make(chan bool)
	waitForSigInt(stopChannel, stopChannel2, stopChannel3)

	var writer screencapture.CmSampleBufConsumer

	pr, pw = io.Pipe()
	fileMode = true

	//buf = new(bytes.Buffer)

	if filename == "" {
		pushSock = setupSockets(pushSpec)
		writer = NewStreamReceiver(pushSock, pw)
		fileMode = false
	} else {
		fh, err := os.Create(filename)
		if err != nil {
			log.Errorf("Error creating file %s:%s", filename, err)
		}
		writer = NewFileReceiver(bufio.NewWriter(fh))
	}

	attempt := 1
	for {
		success := startWithConsumer(writer, udid, stopChannel, stopChannel2)
		if success {
			break
		}
		fmt.Printf("Attempt %d to start streaming\n", attempt)
		if attempt >= 4 {
			log.WithFields(log.Fields{
				"type": "stream_start_failed",
			}).Fatal("Socket new error")
		}
		attempt++
		time.Sleep(time.Second * 1)
	}

	<-stopChannel3
	writer.Stop()
}

func setupSockets(pushSpec string) (pushSock mangos.Socket) {
	var err error
	if pushSock, err = push.NewSocket(); err != nil {
		log.WithFields(log.Fields{
			"type": "err_socket_new",
			"spec": pushSpec,
			"err":  err,
		}).Fatal("Socket new error")
	}
	if err = pushSock.Dial(pushSpec); err != nil {
		log.WithFields(log.Fields{
			"type": "err_socket_connect",
			"spec": pushSpec,
			"err":  err,
		}).Fatal("Socket connect error")
	}

	return pushSock
}

func startWithConsumer(consumer screencapture.CmSampleBufConsumer, udid string, stopChannel chan interface{}, stopChannel2 chan interface{}) bool {
	device, err := FindIosDevice(udid)
	if err != nil {
		printErrJSON(err, "no device found to activate")
		return false
	}

	device, err = EnableQTConfig(device)
	if err != nil {
		printErrJSON(err, "Error enabling QT config")
		return false
	}

	adapter := UsbAdapter{}

	mp := screencapture.NewMessageProcessor(&adapter, stopChannel, consumer, false)

	err = startReading(&adapter, device, &mp, stopChannel2)
	consumer.Stop()
	if err != nil {
		log.Errorf("startReading failure - %s", err)
		log.Info("Closing device")
		return false
	}
	log.Info("Closing device")
	return true
}

func waitForSigInt(stopChannel chan interface{}, stopChannel2 chan interface{}, stopChannel3 chan bool) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for sig := range c {
			fmt.Printf("Got signal %s\n", sig)
			go func() { stopChannel3 <- true }()
			go func() {
				stopChannel2 <- true
				stopChannel2 <- true
			}()
			go func() {
				stopChannel <- true
				stopChannel <- true
			}()

		}
	}()
}

func startReading(usa *UsbAdapter, device IosDevice, receiver screencapture.UsbDataReceiver, stopSignal chan interface{}) error {
	ctx, cleanUp := createContext()
	defer cleanUp()

	usbDevice, err := OpenDevice(ctx, device)
	if err != nil {
		return err
	}
	if !device.IsActivated() {
		return errors.New("device not activated for screen mirroring")
	}
	confignum, _ := usbDevice.ActiveConfigNum()

	log.Debugf("Config is active: %d, QT config is: %d", confignum, device.QTConfigIndex)

	config, err := usbDevice.Config(device.QTConfigIndex)
	if err != nil {
		return errors.New("Could not retrieve config")
	}

	log.Debugf("QT Config is active: %s", config.String())

	val, err := usbDevice.Control(0x02, 0x01, 0, 0x86, make([]byte, 0))
	if err != nil {
		log.Debug("failed control", err)
	}
	log.Debugf("Clear Feature RC: %d", val)

	val, err = usbDevice.Control(0x02, 0x01, 0, 0x05, make([]byte, 0))
	if err != nil {
		log.Debug("failed control", err)
	}
	log.Debugf("Clear Feature RC: %d", val)

	iface, err := grabQuickTimeInterface(config)
	if err != nil {
		log.Debug("could not get Quicktime Interface")
		return err
	}
	log.Debugf("Got QT iface:%s", iface.String())

	inboundBulkEndpointIndex, err := grabInBulk(iface.Setting)
	if err != nil {
		return err
	}
	inEndpoint, err := iface.InEndpoint(inboundBulkEndpointIndex)
	if err != nil {
		log.Error("couldnt get InEndpoint")
		return err
	}
	log.Debugf("Inbound Bulk: %s", inEndpoint.String())

	outboundBulkEndpointIndex, err := grabOutBulk(iface.Setting)
	if err != nil {
		return err
	}
	outEndpoint, err := iface.OutEndpoint(outboundBulkEndpointIndex)
	if err != nil {
		log.Error("couldnt get OutEndpoint")
		return err
	}
	log.Debugf("Outbound Bulk: %s", outEndpoint.String())

	usa.outEndpoint = outEndpoint

	stream, err := inEndpoint.NewStream(4096, 5)
	if err != nil {
		log.Fatal("couldnt create stream")
		return err
	}
	log.Debug("Endpoint claimed")
	log.Infof("Device '%s' USB connection ready, waiting for ping..", device.SerialNumber)

	go func() {
		for {
			buffer := make([]byte, 4)

			n, err := io.ReadFull(stream, buffer)
			if err != nil {
				log.Errorf("Failed reading 4bytes length with err:%s only received: %d", err, n)
				return
			}

			//the 4 bytes header are included in the length, so we need to subtract them
			//here to know how long the payload will be
			length := binary.LittleEndian.Uint32(buffer) - 4
			dataBuffer := make([]byte, length)

			n, err = io.ReadFull(stream, dataBuffer)
			if err != nil {
				log.Errorf("Failed reading payload with err:%s only received: %d/%d bytes", err, n, length)
				return
			}
			receiver.ReceiveData(dataBuffer)
		}
	}()
	if !fileMode {
		go h264ToJpeg()
	}

	<-stopSignal
	receiver.CloseSession()
	log.Info("Closing usb stream")

	err = stream.Close()
	if err != nil {
		log.Error("Error closing stream", err)
	}
	log.Info("Closing usb interface")
	iface.Close()

	log.Info("Closing config")
	_ = config.Close()

	sendQTDisable(usbDevice)

	return nil
}
