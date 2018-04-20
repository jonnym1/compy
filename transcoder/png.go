package transcoder

import (
	"github.com/barnacs/compy/proxy"
	"github.com/chai2010/webp"
	"github.com/pixiv/go-libjpeg/jpeg"
	"image/png"
	"net/http"
	"io"
	"os"
	"os/exec"
	"log"
	"strconv"
	"compress/gzip"
	"bytes"
)

type Png struct{}

func (t *Png) Transcode(w *proxy.ResponseWriter, r *proxy.ResponseReader, headers http.Header) error {
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
		//PASS ZERO
	        w.ReadFrom(r)
                return nil
        }

        if tee.Len() < 1000 {
                // TOO SMALL PASS ORIG
                log.Printf("PNG Pass Small")
                io.Copy(w, &tee)
                return nil
        }

        img, err := png.Decode(&imgin)
        if err != nil {
                // ERROR DECODING
                log.Printf("PNG Decode Err, Through, Len:%d", tee.Len())
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
		jOptions := jpeg.EncoderOptions{
                      Quality:        proxy.Qjpeg,
                      OptimizeCoding: true,
                      ProgressiveMode: true,
                }
		if err = jpeg.Encode(&imgout, img, &jOptions); err != nil {
			// JPEG ENCODE FAIL, using pngquant
			fname := RandStringRunes(8)
                	fnameg := "/tmp/"+fname+".png"
        	        fnamew := "/tmp/"+fname+".qpng"
	                fw, err := os.Create(fnameg)
                	if err != nil {
        	                return err
	                }
                	defer fw.Close()
        	        n, err := io.Copy(fw, &tee)
	                if err != nil {
                	        os.Remove(fnameg)
        	                return err
	                }
			w.Header().Set("Content-Type", "image/png")
                        cmd := exec.Command("pngquant", fnameg, "--force", "--quality",strconv.Itoa(proxy.Qjpeg), "-o", fnamew)
                        stdout, err := cmd.Output()
                        fr, err := os.Open(fnamew)
                        if err != nil {
                                os.Remove(fnameg)
                                return err
                        }
                        defer fr.Close()
                        n2, err := io.Copy(w, fr)
                        if err != nil {
                                os.Remove(fnameg)
                                os.Remove(fnamew)
                        return err
                        }
                        os.Remove(fnameg)
                        os.Remove(fnamew)
                        log.Printf("pngquant IN:%d OUT:%d %s", n, n2,stdout)

		} else {
			if imgout.Len() > tee.Len() {
			// SIZE INCREASED PASS ORIG
				io.Copy(w, &tee)
			} else {
				w.Header().Set("Content-Type", "image/jpeg")
				io.Copy(w, &imgout)
				log.Printf("PNG to JPEG")
			}
		}
	}
	return nil
}
