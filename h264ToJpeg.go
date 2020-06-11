package main

import (
	"bytes"
	"github.com/3d0c/gmf"
	"image"
	"image/jpeg"
	"io"
	"time"

	log "github.com/sirupsen/logrus"
)

func h264ToJpeg() {
	var swsCtx *gmf.SwsCtx

	inputCtx := gmf.NewCtx()
	defer inputCtx.Close()

	avioCtx, err := gmf.NewAVIOContext(inputCtx, &gmf.AVIOHandlers{ReadPacket: reader})
	defer gmf.Release(avioCtx)
	if err != nil {
		log.Fatal(err)
	}
	_ = inputCtx.SetPb(avioCtx).OpenInput("")

	srcVideoStream, err := inputCtx.GetBestStream(gmf.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		log.Printf("No video stream found ")
		return
	}

	codec, err := gmf.FindEncoder(gmf.AV_CODEC_ID_RAWVIDEO)
	if err != nil {
		log.Fatalf("%s\n", err)
	}

	cc := gmf.NewCodecCtx(codec)
	defer gmf.Release(cc)

	cc.SetTimeBase(gmf.AVR{Num: 1, Den: 1})

	cc.SetPixFmt(gmf.AV_PIX_FMT_RGBA).SetWidth(srcVideoStream.CodecCtx().Width() / 2).SetHeight(srcVideoStream.CodecCtx().Height() / 2)
	if codec.IsExperimental() {
		cc.SetStrictCompliance(gmf.FF_COMPLIANCE_EXPERIMENTAL)
	}

	if err := cc.Open(nil); err != nil {
		log.Fatal(err)
	}
	defer cc.Free()

	ist, err := inputCtx.GetStream(srcVideoStream.Index())
	if err != nil {
		log.Fatalf("Error getting stream - %s\n", err)
	}
	defer ist.Free()

	// convert source pix_fmt into AV_PIX_FMT_RGBA
	// which is set up by codec context above
	icc := srcVideoStream.CodecCtx()
	if swsCtx, err = gmf.NewSwsCtx(icc.Width(), icc.Height(), icc.PixFmt(), cc.Width(), cc.Height(), cc.PixFmt(), gmf.SWS_BICUBIC); err != nil {
		panic(err)
	}
	defer swsCtx.Free()

	start := time.Now()

	var (
		pkt        *gmf.Packet
		frames     []*gmf.Frame
		drain      int = -1
		frameCount int = 0
	)

	for {
		if drain >= 0 {
			break
		}

		pkt, err = inputCtx.GetNextPacket()
		if err != nil && err != io.EOF {
			if pkt != nil {
				pkt.Free()
			}
			log.Printf("error getting next packet - %s", err)
			break
		} else if err != nil && pkt == nil {
			drain = 0
		}

		if pkt != nil && pkt.StreamIndex() != srcVideoStream.Index() {
			continue
		}

		frames, err = ist.CodecCtx().Decode(pkt)
		if err != nil {
			log.Printf("Fatal error during decoding - %s\n", err)
			break
		}

		// Decode() method doesn't treat EAGAIN and EOF as errors
		// it returns empty frames slice instead. Countinue until
		// input EOF or frames received.
		if len(frames) == 0 && drain < 0 {
			continue
		}

		if frames, err = gmf.DefaultRescaler(swsCtx, frames); err != nil {
			panic(err)
		}

		encode(cc, frames, drain)

		for i := range frames {
			frames[i].Free()
			frameCount++
		}

		if pkt != nil {
			pkt.Free()
			pkt = nil
		}
	}

	for i := 0; i < inputCtx.StreamsCnt(); i++ {
		st, _ := inputCtx.GetStream(i)
		st.CodecCtx().Free()
		st.Free()
	}

	since := time.Since(start)
	log.Printf("Finished in %v, avg %.2f fps", since, float64(frameCount)/since.Seconds())

}

func reader() ([]byte, int) {
	var (
		err       error
		bytesread int
	)

	pos := 0
	buf := make([]byte, 50000)
	for {
		bytesread, err = pr.Read(buf[pos:])
		if bytesread > 0 {
			pos += bytesread
		}

		if pos >= len(buf) || pos > 0 {
			//log.Printf("read: %d\n", len(buf[:pos]))
			pos = 0
			break
		}

		if err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			break
		}
	}

	return buf, bytesread
}

func encode(cc *gmf.CodecCtx, frames []*gmf.Frame, drain int) {
	packets, err := cc.Encode(frames, drain)
	if err != nil {
		log.Fatalf("Error encoding - %s\n", err)
	}
	if len(packets) == 0 {
		return
	}

	for _, p := range packets {
		width, height := cc.Width(), cc.Height()

		img := new(image.RGBA)
		img.Pix = p.Data()
		img.Stride = 4 * width
		img.Rect = image.Rect(0, 0, width, height)

		result := int64(0)

		if prevImg != nil {
			if result, err = FastCompare(img, prevImg); err != nil {
				log.Fatal(err)
			}
		}

		if result > 500 || prevImg == nil {
			log.Printf("compare result : %d\n", result)
			sendImage(img)
			prevImg = img
		}

		p.Free()
		continue
	}

	return
}

func sendImage(b image.Image) {
	//name := fmt.Sprintf("tmp/%d.jpg", fileCount)
	//fp, err := os.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	//if err != nil {
	//	log.Fatalf("Error opening file '%s' - %s\n", name, err)
	//}
	//defer fp.Close()
	//
	//fileCount++
	//log.Printf("Saving file %s\n", name)
	//if err = jpeg.Encode(fp, b, &jpeg.Options{Quality: 75}); err != nil {
	//	log.Fatal(err)
	//}

	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, b, nil); err != nil {
		log.Fatal(err)
	}
	err := pushSock.Send(buf.Bytes())
	if err != nil {
		log.Fatal(err)
	}
}
