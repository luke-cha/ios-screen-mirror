package main

import (
	"bytes"
	"encoding/binary"
	cm "github.com/danielpaulus/quicktime_video_hack/screencapture/coremedia"
	"go.nanomsg.org/mangos/v3"
	"io"
	// register transports
	_ "go.nanomsg.org/mangos/v3/transport/all"
)

var startCode = []byte{00, 00, 00, 01}

//ZMQWriter writes nalus into a file using 0x00000001 as a separator (h264 ANNEX B) and raw pcm audio into a wav file
type IOSImageReceiver struct {
	socket mangos.Socket
	buffer bytes.Buffer
	fh     io.Writer
	pw     *io.PipeWriter
}

func NewStreamReceiver(socket mangos.Socket, pw *io.PipeWriter) IOSImageReceiver {
	return IOSImageReceiver{socket: socket, pw: pw}
}

func NewFileReceiver(fh io.Writer) IOSImageReceiver {
	return IOSImageReceiver{fh: fh}
}

//Consume writes PPS and SPS as well as sample bufs into a annex b .h264 file and audio samples into a wav file
func (self IOSImageReceiver) Consume(buf cm.CMSampleBuffer) error {
	if buf.MediaType == cm.MediaTypeSound {
		return self.consumeAudio(buf)
	}
	return self.consumeVideo(buf)
}

func (self IOSImageReceiver) Stop() {}

func (self IOSImageReceiver) consumeVideo(buf cm.CMSampleBuffer) error {
	if buf.HasFormatDescription {
		err := self.writeNalu(buf.FormatDescription.PPS)
		if err != nil {
			return err
		}
		err = self.writeNalu(buf.FormatDescription.SPS)
		if err != nil {
			return err
		}
	}
	if !buf.HasSampleData() {
		return nil
	}
	return self.writeNalus(buf.SampleData)
}

func (self IOSImageReceiver) writeNalus(bytes []byte) error {
	slice := bytes
	for len(slice) > 0 {
		length := binary.BigEndian.Uint32(slice)
		err := self.writeNalu(slice[4 : length+4])
		if err != nil {
			return err
		}
		slice = slice[length+4:]
	}
	return nil
}

func (self IOSImageReceiver) writeNalu(naluBytes []byte) error {
	_, err := self.buffer.Write(startCode)
	if err != nil {
		return err
	}
	_, err = self.buffer.Write(naluBytes)
	if err != nil {
		return err
	}

	////여기서 부터 frame 하나씩
	if self.fh == nil {
		_, _ = io.Copy(self.pw, newAlphaReader(self.buffer))

	} else {
		_, _ = self.fh.Write(self.buffer.Bytes())
	}
	self.buffer.Reset()
	return nil
}

func (self IOSImageReceiver) consumeAudio(buffer cm.CMSampleBuffer) error {
	return nil
}

type trickle struct {
	counter int
	buffer  bytes.Buffer
}

func newAlphaReader(buffer bytes.Buffer) *trickle {
	return &trickle{buffer: buffer}
}

func (t *trickle) Read(p []byte) (n int, err error) {
	n, err = t.buffer.Read(p)
	if err != nil {
		return n, err
	}
	buf := make([]byte, n)
	copy(buf, p)
	return n, nil
}
