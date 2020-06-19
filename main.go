package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/danielpaulus/quicktime_video_hack/screencapture"
	"github.com/google/gousb"

	"go.nanomsg.org/mangos/v3"
	"go.nanomsg.org/mangos/v3/protocol/push"
	// register transports
	_ "github.com/nanomsg/mangos/transport/all"

	log "github.com/sirupsen/logrus"
)

var (
	pr       *io.PipeReader
	pw       *io.PipeWriter
	pushSock mangos.Socket
	prevImg  *image.RGBA
	fileMode bool
)

func main() {
	var udid = flag.String("udid", "", "Device UDID")
	var devicesCmd = flag.Bool("devices", false, "List devices then exit")
	var pullCmd = flag.Bool("pull", false, "Pull video")
	var pushSpec = flag.String("pushSpec", "tcp://127.0.0.1:7879", "push image to tcp address")
	var file = flag.String("file", "", "File to save h264 nalus into")
	var verbose = flag.Bool("v", false, "Verbose Debugging")
	var enableCmd = flag.Bool("enable", false, "Enable device")
	var disableCmd = flag.Bool("disable", false, "Disable device")
	flag.Parse()

	log.SetFormatter(&log.JSONFormatter{})

	if *verbose {
		log.Info("Set Debug mode")
		log.SetLevel(log.DebugLevel)
	}

	if *devicesCmd {
		devices()
		return
	} else if *pullCmd {
		gopull(*pushSpec, *file, *udid)
	} else if *enableCmd {
		enable(*udid)
	} else if *disableCmd {
		disable(*udid)
	} else {
		flag.Usage()
	}
}

func devices() {
	ctx := gousb.NewContext()

	devs, err := findIosDevices(ctx)
	if err != nil {
		log.Errorf("Error finding iOS Devices - %s", err)
	}

	for _, dev := range devs {
		serial := stripSerial(dev)
		product, _ := dev.Product()
		subcs := getVendorSubclasses(dev.Desc)
		activated := 0
		for _, subc := range subcs {
			if int(subc) == 42 {
				activated = 1
			}
		}
		fmt.Printf("Bus: %d, Address: %d, Port: %d, UDID:%s, Name:%s, VID=%s, PID=%s, Activated=%d\n", dev.Desc.Bus, dev.Desc.Address, dev.Desc.Port, serial, product, dev.Desc.Vendor, dev.Desc.Product, activated)
		_ = dev.Close()
	}

	_ = ctx.Close()
}

func openDevice(ctx *gousb.Context, uuid string) (*gousb.Device, bool) {
	devs, err := findIosDevices(ctx)
	if err != nil {
		log.Errorf("Error finding iOS Devices - %s", err)
	}
	var foundDevice *gousb.Device = nil
	activated := false
	for _, dev := range devs {
		serial := stripSerial(dev)
		serial = stripCtlFromBytes(serial)
		if serial == uuid {
			foundDevice = dev
			subcs := getVendorSubclasses(dev.Desc)
			for _, subc := range subcs {
				if int(subc) == 42 {
					activated = true
				}
			}
		} else {
			_ = dev.Close()
		}
	}
	return foundDevice, activated
}

func stripSerial(usb *gousb.Device) string {
	str, _ := usb.SerialNumber()
	return stripCtlFromBytes(str)
}

func findIosDevices(ctx *gousb.Context) ([]*gousb.Device, error) {
	return ctx.OpenDevices(func(dev *gousb.DeviceDesc) bool {
		for _, subc := range getVendorSubclasses(dev) {
			if subc == gousb.ClassApplication {
				return true
			}
		}
		return false
	})
}

func getVendorSubclasses(desc *gousb.DeviceDesc) []gousb.Class {
	subClasses := []gousb.Class{}
	for _, conf := range desc.Configs {
		for _, iface := range conf.Interfaces {
			if iface.AltSettings[0].Class == gousb.ClassVendorSpec {
				subClass := iface.AltSettings[0].SubClass
				subClasses = append(subClasses, subClass)
			}
		}
	}
	return subClasses
}

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

func enable(udid string) {
	ctx := gousb.NewContext()

	var usbDevice *gousb.Device = nil
	var activated bool
	if udid == "" {
		devs, err := findIosDevices(ctx)
		if err != nil {
			log.Errorf("Error finding iOS Devices - %s", err)
		}
		for _, dev := range devs {
			oneActivated := false
			oneUdid := ""
			if usbDevice == nil {
				oneUdid = stripSerial(dev)
				subCs := getVendorSubclasses(dev.Desc)
				for _, subc := range subCs {
					if int(subc) == 42 {
						oneActivated = true
					}
				}
				if oneActivated == false {
					usbDevice = dev
					udid = oneUdid
					activated = oneActivated
				} else {
					_ = dev.Close()
				}
			} else {
				_ = dev.Close()
			}
		}
		if udid != "" {
			log.Infof("Using first disabled device; uuid=%s", udid)
		}
	} else {
		usbDevice, activated = openDevice(ctx, udid)
		log.Info("Opened device")
	}

	if usbDevice == nil {
		log.Info("Could not find a disabled device to activate")
		_ = ctx.Close()
		return
	}

	if activated == true {
		log.Info("Device already enabled")
		_ = usbDevice.Close()
		_ = ctx.Close()
		return
	}

	sendQTEnable(usbDevice)

	_ = usbDevice.Close()
	_ = ctx.Close()
}

func disable(udid string) {
	ctx := gousb.NewContext()

	var usbDevice *gousb.Device = nil
	var activated bool
	if udid == "" {
		devs, err := findIosDevices(ctx)
		if err != nil {
			log.Errorf("Error finding iOS Devices - %s", err)
		}
		for _, dev := range devs {
			oneActivated := true
			oneUdid := ""
			if usbDevice == nil {
				oneUdid = stripSerial(dev)
				subcs := getVendorSubclasses(dev.Desc)
				for _, subc := range subcs {
					if int(subc) == 42 {
						oneActivated = true
					}
				}
				if oneActivated == true {
					usbDevice = dev
					udid = oneUdid
					activated = oneActivated
				} else {
					_ = dev.Close()
				}
			} else {
				_ = dev.Close()
			}
		}
		if udid != "" {
			log.Infof("Using first enabled device; uuid=%s", udid)
		}
	} else {
		usbDevice, activated = openDevice(ctx, udid)

		log.Info("Opened device")
	}

	if usbDevice == nil {
		log.Info("Could not find a enabled device to disabled")
		_ = ctx.Close()
		return
	}

	if activated == false {
		log.Info("Device already disabled")
		_ = usbDevice.Close()
		_ = ctx.Close()
		return
	}

	sendQTDisable(usbDevice)

	_ = usbDevice.Close()
	_ = ctx.Close()
}

func startWithConsumer(consumer screencapture.CmSampleBufConsumer, udid string, stopChannel chan interface{}, stopChannel2 chan interface{}) bool {
	ctx := gousb.NewContext()

	var usbDevice *gousb.Device = nil
	var activated bool = false
	if udid == "" {
		devs, err := findIosDevices(ctx)
		if err != nil {
			log.Errorf("Error finding iOS Devices - %s", err)
		}
		for _, dev := range devs {
			if usbDevice == nil {
				udid = stripSerial(dev)
				subcs := getVendorSubclasses(dev.Desc)
				for _, subc := range subcs {
					if int(subc) == 42 {
						activated = true
					}
				}
				usbDevice = dev
			} else {
				dev.Close()
			}
		}
	} else {
		usbDevice, activated = openDevice(ctx, udid)
		log.Info("Opened device")
	}

	if !activated {
		log.Info("Not activated; attempting to activate")
		sendQTEnable(usbDevice)

		var i int = 0
		for {
			time.Sleep(500 * time.Millisecond)
			var activated bool
			usbDevice, activated = openDevice(ctx, udid)
			if activated {
				break
			}
			i++
			if i > 5 {
				log.Debug("Failed activating config")
				return false
			}
		}
	}

	adapter := UsbAdapter{}

	mp := screencapture.NewMessageProcessor(&adapter, stopChannel, consumer)

	err := startReading(&adapter, usbDevice, &mp, stopChannel2)
	if err != nil {
		log.Errorf("startReading failure - %s", err)
		log.Info("Closing device")
		_ = usbDevice.Close()
		_ = ctx.Close()
		return false
	}

	log.Info("Closing device")
	_ = usbDevice.Close()
	_ = ctx.Close()

	return true
}

func sendQTEnable(device *gousb.Device) {
	val, err := device.Control(0x40, 0x52, 0x00, 0x02, []byte{})
	if err != nil {
		log.Warnf("Failed sending control transfer for enabling hidden QT config. Seems like this happens sometimes but it still works usually: %d, %s", val, err)
	}
	log.Debugf("Enabling QT config RC:%d", val)
}

func sendQTDisable(device *gousb.Device) {
	val, err := device.Control(0x40, 0x52, 0x00, 0x00, []byte{})
	if err != nil {
		log.Warnf("Failed sending control transfer for enabling hidden QT config. Seems like this happens sometimes but it still works usually: %d, %s", val, err)
	}
	log.Debugf("Dsiabling QT config RC:%d", val)
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

// Stuff below more or less copied from quicktime_video_hack/screencapture/usbadapter.go and other files in that directory
// All of these stuff has to be copied in order to alter startReading due to non-exposed functions and variables

type UsbAdapter struct {
	outEndpoint *gousb.OutEndpoint
}

func (usa UsbAdapter) WriteDataToUsb(bytes []byte) {
	_, err := usa.outEndpoint.Write(bytes)
	if err != nil {
		log.Error("failed sending to usb", err)
	}
}

func startReading(usa *UsbAdapter, usbDevice *gousb.Device, receiver screencapture.UsbDataReceiver, stopSignal chan interface{}) error {
	var confignum int = 6

	config, err := usbDevice.Config(confignum)
	if err != nil {
		return errors.New("Could not retrieve config")
	}

	log.Debugf("QT Config is active: %s", config.String())

	/*val, err := usbDevice.Control(0x02, 0x01, 0, 0x86, make([]byte, 0))
	  if err != nil {
	      log.Debug("failed control", err)
	  }
	  log.Debugf("Clear Feature RC: %d", val)

	  val, err = usbDevice.Control(0x02, 0x01, 0, 0x05, make([]byte, 0))
	  if err != nil {
	      log.Debug("failed control", err)
	  }
	  log.Debugf("Clear Feature RC: %d", val)*/

	success, iface := findInterfaceForSubclass(config, 0x2a)
	if !success {
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
	udid, _ := usbDevice.SerialNumber()
	log.Infof("Device '%s' ready to stream ( click 'Settings-Developer-Reset Media Services' if nothing happens )", udid)

	go func() {
		frameExtractor := screencapture.NewLengthFieldBasedFrameExtractor()
		for {
			buffer := make([]byte, 65536)

			n, err := stream.Read(buffer)
			if err != nil {
				log.Error("couldn't read bytes", err)
				return
			}

			frame, isCompleteFrame := frameExtractor.ExtractFrame(buffer[:n])
			if isCompleteFrame {
				receiver.ReceiveData(frame)
			}
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

func grabOutBulk(setting gousb.InterfaceSetting) (int, error) {
	for _, v := range setting.Endpoints {
		if v.Direction == gousb.EndpointDirectionOut {
			return v.Number, nil
		}
	}
	return 0, errors.New("Outbound Bulkendpoint not found")
}

func grabInBulk(setting gousb.InterfaceSetting) (int, error) {
	for _, v := range setting.Endpoints {
		if v.Direction == gousb.EndpointDirectionIn {
			return v.Number, nil
		}
	}
	return 0, errors.New("Inbound Bulkendpoint not found")
}

func findInterfaceForSubclass(config *gousb.Config, subClass gousb.Class) (bool, *gousb.Interface) {
	for _, ifaced := range config.Desc.Interfaces {
		if ifaced.AltSettings[0].Class == gousb.ClassVendorSpec &&
			ifaced.AltSettings[0].SubClass == subClass {
			iface, _ := config.Interface(ifaced.Number, 0)
			return true, iface
		}
	}
	return false, nil
}
