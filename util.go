package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/gousb"
	log "github.com/sirupsen/logrus"
	"image"
	"math"
	"time"
)

const (
	//UsbMuxSubclass is the subclass used for USBMux USB configuration.
	UsbMuxSubclass = gousb.ClassApplication
	//QuicktimeSubclass is the subclass used for the Quicktime USB configuration.
	QuicktimeSubclass gousb.Class = 0x2A
)

func FastCompare(img1, img2 *image.RGBA) (int64, error) {
	if img1.Bounds() != img2.Bounds() {
		return 0, fmt.Errorf("image bounds not equal: %+v, %+v", img1.Bounds(), img2.Bounds())
	}

	accumError := int64(0)

	for i := 0; i < len(img1.Pix); i++ {
		accumError += int64(sqDiffUInt8(img1.Pix[i], img2.Pix[i]))
	}

	return int64(math.Sqrt(float64(accumError))), nil
}

func sqDiffUInt8(x, y uint8) uint64 {
	d := uint64(x) - uint64(y)
	return d * d
}

func stripCtlFromBytes(str string) string {
	b := make([]byte, len(str))
	var bl int
	for i := 0; i < len(str); i++ {
		c := str[i]
		if c >= 32 && c != 127 {
			b[bl] = c
			bl++
		}
	}
	return string(b[:bl])
}

func printErrJSON(err error, msg string) {
	printJSON(map[string]interface{}{
		"original_error": err.Error(),
		"error_message":  msg,
	})
}
func printJSON(output map[string]interface{}) {
	text, err := json.Marshal(output)
	if err != nil {
		log.Fatalf("Broken json serialization, error: %s", err)
	}
	println(string(text))
}

func isQtConfig(confDesc gousb.ConfigDesc) bool {
	b, _ := findInterfaceForSubclass(confDesc, QuicktimeSubclass)
	return b
}

func isMuxConfig(confDesc gousb.ConfigDesc) bool {
	b, _ := findInterfaceForSubclass(confDesc, UsbMuxSubclass)
	return b
}

func findConfigurations(desc *gousb.DeviceDesc) (int, int) {
	var muxConfigIndex = -1
	var qtConfigIndex = -1

	for _, v := range desc.Configs {
		if isMuxConfig(v) && !isQtConfig(v) {
			muxConfigIndex = v.Number
			log.Debugf("Found MuxConfig %d for Device %s", muxConfigIndex, desc.String())
		}
		if isQtConfig(v) {
			qtConfigIndex = v.Number
			log.Debugf("Found QTConfig %d for Device %s", qtConfigIndex, desc.String())
		}
	}
	return muxConfigIndex, qtConfigIndex
}

func mapToIosDevice(devices []*gousb.Device) ([]IosDevice, error) {
	iosDevices := make([]IosDevice, len(devices))
	for i, d := range devices {
		log.Debugf("Getting serial for: %s", d.String())
		serial, err := d.SerialNumber()
		log.Debug("Got serial" + serial)
		if err != nil {
			return nil, err
		}
		product, err := d.Product()
		if err != nil {
			return nil, err
		}

		muxConfigIndex, qtConfigIndex := findConfigurations(d.Desc)
		iosDevice := IosDevice{serial, product, muxConfigIndex, qtConfigIndex, d.Desc.Vendor, d.Desc.Product, d.String()}
		d.Close()
		iosDevices[i] = iosDevice

	}
	return iosDevices, nil
}

func grabQuickTimeInterface(config *gousb.Config) (*gousb.Interface, error) {
	log.Debug("Looking for quicktime interface..")
	found, ifaceIndex := findInterfaceForSubclass(config.Desc, QuicktimeSubclass)
	if !found {
		return nil, fmt.Errorf("did not find interface %v", config)
	}
	log.Debugf("Found Quicktimeinterface: %d", ifaceIndex)
	return config.Interface(ifaceIndex, 0)
}

func isValidIosDevice(desc *gousb.DeviceDesc) bool {
	muxConfigIndex, _ := findConfigurations(desc)
	if muxConfigIndex == -1 {
		return false
	}
	return true
}

// FindIosDevice finds a iOS device by udid or picks the first one if udid == ""
func FindIosDevice(udid string) (IosDevice, error) {
	ctx, cleanUp := createContext()
	defer cleanUp()
	list, err := findIosDevices(ctx, isValidIosDevice)
	if err != nil {
		return IosDevice{}, err
	}
	if len(list) == 0 {
		return IosDevice{}, errors.New("no iOS devices are connected to this host")
	}
	if udid == "" {
		log.Infof("no udid specified, using '%s'", list[0].SerialNumber)
		return list[0], nil
	}
	for _, device := range list {
		if udid == device.SerialNumber {
			return device, nil
		}
	}
	return IosDevice{}, fmt.Errorf("device with udid:'%s' not found", udid)
}

func findIosDevices(ctx *gousb.Context, validDeviceChecker func(desc *gousb.DeviceDesc) bool) ([]IosDevice, error) {
	devices, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		// this function is called for every device present.
		// Returning true means the device should be opened.
		return validDeviceChecker(desc)
	})
	if err != nil {
		return nil, err
	}
	iosDevices, err := mapToIosDevice(devices)
	if err != nil {
		return nil, err
	}

	return iosDevices, nil
}

func OpenDevice(ctx *gousb.Context, iosDevice IosDevice) (*gousb.Device, error) {
	deviceList, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return true
	})

	if err != nil {
		log.Warn("Error opening usb devices", err)
	}
	var usbDevice *gousb.Device = nil
	for _, device := range deviceList {
		sn, err := device.SerialNumber()
		if err != nil {
			log.Warn("Error retrieving Serialnumber", err)
		}
		if sn == iosDevice.SerialNumber {
			usbDevice = device
		} else {
			device.Close()
		}
	}

	if usbDevice == nil {
		return nil, fmt.Errorf("Unable to find device:%+v", iosDevice)
	}
	return usbDevice, nil
}

// EnableQTConfig enables the hidden QuickTime Device configuration that will expose two new bulk endpoints.
// We will send a control transfer to the device via USB which will cause the device to disconnect and then
// re-connect with a new device configuration. Usually the usbmuxd will automatically enable that new config
// as it will detect it as the device's preferredConfig.
func EnableQTConfig(device IosDevice) (IosDevice, error) {
	udid := device.SerialNumber
	ctx := gousb.NewContext()
	usbDevice, err := OpenDevice(ctx, device)
	if err != nil {
		return IosDevice{}, err
	}
	if isValidIosDeviceWithActiveQTConfig(usbDevice.Desc) {
		log.Debugf("Skipping %s because it already has an active QT config", udid)
		return device, nil
	}

	sendQTConfigControlRequest(usbDevice)

	var i int
	for {
		log.Debugf("Checking for active QT config for %s", udid)

		err = ctx.Close()
		if err != nil {
			log.Warn("failed closing context", err)
		}
		time.Sleep(500 * time.Millisecond)
		log.Debug("Reopening Context")
		ctx = gousb.NewContext()
		device, err = device.ReOpen(ctx)
		if err != nil {
			log.Debugf("device not found:%s", err)
			continue
		}
		i++
		if i > 10 {
			log.Debug("Failed activating config")
			return IosDevice{}, fmt.Errorf("could not activate Quicktime Config for %s", udid)
		}
		break
	}
	log.Debugf("QTConfig for %s activated", udid)
	return device, err
}

func sendQTDisable(device *gousb.Device) {
	val, err := device.Control(0x40, 0x52, 0x00, 0x00, []byte{})
	if err != nil {
		log.Warnf("Failed sending control transfer for enabling hidden QT config. Seems like this happens sometimes but it still works usually: %d, %s", val, err)
	}
	log.Debugf("Dsiabling QT config RC:%d", val)
}

func isValidIosDeviceWithActiveQTConfig(desc *gousb.DeviceDesc) bool {
	_, qtConfigIndex := findConfigurations(desc)
	if qtConfigIndex == -1 {
		return false
	}
	return true
}

func sendQTConfigControlRequest(device *gousb.Device) {
	response := make([]byte, 0)
	val, err := device.Control(0x40, 0x52, 0x00, 0x02, response)
	if err != nil {
		log.Warnf("Failed sending control transfer for enabling hidden QT config. Seems like this happens sometimes but it still works usually: %s", err)
	}
	log.Debugf("Enabling QT config RC:%d", val)
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

func findInterfaceForSubclass(confDesc gousb.ConfigDesc, subClass gousb.Class) (bool, int) {
	for _, iface := range confDesc.Interfaces {
		//usually the interfaces we care about have only one altsetting

		for _, alt := range iface.AltSettings {
			isVendorClass := alt.Class == gousb.ClassVendorSpec
			isCorrectSubClass := alt.SubClass == subClass
			log.Debugf("found: %t", isCorrectSubClass && isVendorClass)

		}
		isVendorClass := iface.AltSettings[0].Class == gousb.ClassVendorSpec
		isCorrectSubClass := iface.AltSettings[0].SubClass == subClass

		log.Debugf("iface:%v altsettings:%d isvendor:%t isub:%t", iface, len(iface.AltSettings), isVendorClass, isCorrectSubClass)
		if isVendorClass && isCorrectSubClass {
			return true, iface.Number
		}
	}
	return false, -1
}

func createContext() (*gousb.Context, func()) {
	ctx := gousb.NewContext()
	log.Debugf("Opened usbcontext:%v", ctx)
	cleanUp := func() {
		err := ctx.Close()
		if err != nil {
			log.Fatalf("Error closing usb context: %v", ctx)
		}
	}
	return ctx, cleanUp
}

//IosDevice contains a gousb.Device pointer for a found device and some additional info like the device udid
type IosDevice struct {
	SerialNumber      string
	ProductName       string
	UsbMuxConfigIndex int
	QTConfigIndex     int
	VID               gousb.ID
	PID               gousb.ID
	UsbInfo           string
}

//ReOpen creates a new Ios device, opening it using VID and PID, using the given context
func (d IosDevice) ReOpen(ctx *gousb.Context) (IosDevice, error) {

	dev, err := OpenDevice(ctx, d)
	if err != nil {
		return IosDevice{}, err
	}
	idev, err := mapToIosDevice([]*gousb.Device{dev})
	if err != nil {
		return IosDevice{}, err
	}
	return idev[0], nil
}

//IsActivated returns a boolean that is true when this device was enabled for screen mirroring and false otherwise.
func (d *IosDevice) IsActivated() bool {
	return d.QTConfigIndex != -1
}

//DetailsMap contains all the info for a device in a map ready to be JSON encoded
func (d *IosDevice) DetailsMap() map[string]interface{} {
	return map[string]interface{}{
		"deviceName":               d.ProductName,
		"usb_device_info":          d.UsbInfo,
		"udid":                     d.SerialNumber,
		"screen_mirroring_enabled": d.IsActivated(),
	}
}

func (d *IosDevice) String() string {
	return fmt.Sprintf("'%s'  %s serial: %s, qt_mode:%t", d.ProductName, d.UsbInfo, d.SerialNumber, d.IsActivated())
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
