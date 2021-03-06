package transcoder

import (
	"github.com/barnacs/compy/proxy"
	"github.com/chai2010/webp"
	"github.com/pixiv/go-libjpeg/jpeg"
	"net/http"
	"log"
	"io"
	"bytes"
	"compress/gzip"
)

type Jpeg struct {
	decOptions *jpeg.DecoderOptions
	encOptions *jpeg.EncoderOptions
}

func NewJpeg(quality int) *Jpeg {
	return &Jpeg{
		decOptions: &jpeg.DecoderOptions{},
		encOptions: &jpeg.EncoderOptions{
			Quality:        quality,
			OptimizeCoding: true,
			ProgressiveMode: true,
		},
	}
}


func (t *Jpeg) Transcode(w *proxy.ResponseWriter, r *proxy.ResponseReader, headers http.Header) error {
	if r.Header().Get("Content-Encoding") == "gzip" {
                gzr, err := gzip.NewReader(r.Reader)
                if err != nil {
                        return err
                }
                defer gzr.Close()
                r.Reader = gzr
                r.Header().Del("Content-Encoding")
                w.Header().Del("Content-Encoding")
        }

	var tee bytes.Buffer
	var imgin bytes.Buffer
	io.Copy(&imgin, io.TeeReader(r, &tee))

        if tee.Len() == 0 {
		// PASS ZERO
		w.ReadFrom(r)
                return nil
        }

	if tee.Len() < 1000 {
                // TOO SMALL PASS ORIG
                log.Printf("JPEG Pass Small")
                io.Copy(w, &tee)
                return nil
        }
	img, err := jpeg.Decode(&imgin, t.decOptions)
	if err != nil {
		// ERROR DECODING
		log.Printf("JPEG Decode Err, Through, Len:%d", tee.Len())
		io.Copy(w, &tee)
		return nil
	}


	var imgout bytes.Buffer
	if SupportsWebP(headers) {
		w.Header().Set("Content-Type", "image/webp")
		options := webp.Options{
			Lossless: false,
			Quality:  float32(proxy.Qjpeg),
		}
		if err = webp.Encode(&imgout, img, &options); err != nil {
			log.Printf("WEBP Encode Err, Through, Len:%d", tee.Len())
	                io.Copy(w, &tee)
		} else {
			if imgout.Len() > tee.Len() {
				// SIZE INCREASED PASS ORIG
				io.Copy(w, &tee)
			} else {
				io.Copy(w, &imgout)
			}
		}
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
		encOptions := t.encOptions
		if err = jpeg.Encode(&imgout, img, encOptions); err != nil {
			log.Printf("JPEG Encode Err, Through, Len:%d", tee.Len())
                        io.Copy(w, &tee)
                } else {
			if imgout.Len() > tee.Len() {
				// SIZE INCREASED PASS ORIG
                                io.Copy(w, &tee)
                        } else {
                                io.Copy(w, &imgout)
                        }
		}
		log.Printf("JPEG to JPEG")
	}
	return nil
}
