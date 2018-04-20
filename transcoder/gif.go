package transcoder

import (
	"github.com/barnacs/compy/proxy"
	"image/gif"
	"github.com/chai2010/webp"
	"net/http"
	"io"
	"os"
	"os/exec"
	"log"
	"time"
	"math/rand"
        "image/png"
	"strconv"
	"bytes"
	"compress/gzip"
)

type Gif struct{}

func init() {
    rand.Seed(time.Now().UnixNano())
}

var letterRunes = []rune("1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandStringRunes(n int) string {
    b := make([]rune, n)
    for i := range b {
        b[i] = letterRunes[rand.Intn(len(letterRunes))]
    }
    return string(b)
}

func (t *Gif) Transcode(w *proxy.ResponseWriter, r *proxy.ResponseReader, headers http.Header) error {
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

        if SupportsWebP(headers) && proxy.Qjpeg != 10 {
		w.Header().Set("Content-Type", "image/webp")
		// SAVE GIF TO FILE
		fname := RandStringRunes(8)
		fnameg := "/tmp/"+fname+".gif"
		fnamew := "/tmp/"+fname+".webp"
		fw, err := os.Create(fnameg)
		if err != nil {
	        	return err
		}
		defer fw.Close()
		n, err := io.Copy(fw, r)
		if err != nil {
			os.Remove(fnameg)
		        return err
		}
		// CHECK SIZE
		fi, err := fw.Stat()
		if err != nil {
			os.Remove(fnameg)
                        return err
		}
		if fi.Size() == 0 {
		// PASS ZERO
			os.Remove(fnameg)
                	w.ReadFrom(r)
	                return nil
        	}
		if fi.Size() < 2000 {
		// PASS THROUGH IF TOO SMALL
			log.Printf("GIF Pass Small")
			fp, err := os.Open(fnameg)
			if err == nil {
				defer fp.Close()
				io.Copy(w, fp)
				os.Remove(fnameg)
				return nil
			} else {
				return err
			}
		}
		// TRANSCODE
		cmd := exec.Command("gif2webp", "-lossy", "-q", strconv.Itoa(proxy.Qjpeg), "-m", "4", fnameg, "-o", fnamew)
		_, _ = cmd.Output()
		// OPEN WEBP IMAGE
		fr, err := os.Open(fnamew)
		if err != nil {
			// DID NOT TRANSCODE try cwebp static image
			log.Printf("gif2webp fail, using cwebp")
			cmd := exec.Command("cwebp", fnameg, "-q", strconv.Itoa(proxy.Qjpeg), "-o", fnamew)
			_, _ = cmd.Output()
			// OPEN WEBP IMAGE try again
	                fr, err = os.Open(fnamew)
			if err != nil {
				os.Remove(fnameg)
				log.Printf("GIF Decode fail", fr)
		        	return err
			}
		}
		defer fr.Close()
		// CHECK OUTPUT SIZE
		fo, err := fr.Stat()
                if err != nil {
			os.Remove(fnameg)
                        os.Remove(fnamew)
                        return err
                }
                if fo.Size() > fi.Size() {
                // PASS THROUGH IF SIZE INCREASED
                        log.Printf("GIF Pass Size Increased")
                        fp, err := os.Open(fnameg)
                        if err == nil {
                                defer fp.Close()
                                io.Copy(w, fp)
                                os.Remove(fnameg)
                                return nil
                        } else {
                                return err
                        }
                }
		// READ+FORWARD WEBP
		n2, err := io.Copy(w, fr)
		os.Remove(fnameg)
                os.Remove(fnamew)
                if err != nil {
			return err
                }
		log.Printf("gif2webp IN:%d OUT:%d", n, n2)
	} else {
	        var tee bytes.Buffer
	        var imgin bytes.Buffer
        	io.Copy(&imgin, io.TeeReader(r, &tee))

		if tee.Len() == 0 {
                	w.ReadFrom(r)
	                return nil
        	}

		if tee.Len() < 1000 {
                        // PASS THROUGH IF TOO SMALL
                        log.Printf("GIF Pass Small")
                        io.Copy(w, &tee)
                        return nil
                }

		img, err := gif.Decode(&imgin)
      		if err != nil {
        	       	return err
	        }

		var imgout bytes.Buffer
		if SupportsWebP(headers) {
			w.Header().Set("Content-Type", "image/webp")
	                options := webp.Options{
                        	Lossless: false,
	                        Quality:  float32(proxy.Qjpeg),
        	        }
                	if err = webp.Encode(&imgout, img, &options); err != nil {
                        	return err
	                } else {
        	                if imgout.Len() > tee.Len() {
	                                // SIZE INCREASED PASS ORIG
                                	io.Copy(w, &tee)
                        	} else {
                	                io.Copy(w, &imgout)
                        }
                }

			log.Printf("GIF to Static WEBP")
		} else {
			var Enc png.Encoder
        	        Enc.CompressionLevel = -3
                	w.Header().Set("Content-Type", "image/png")
	                if err = Enc.Encode(&imgout, img); err != nil {
        	                return err
			} else {
				if imgout.Len() > tee.Len() {
        	                        // PASS THROUGH IF SIZE INCREASED
					io.Copy(w, &tee)
	                        } else {
        	                        io.Copy(w, &imgout)
                	        }
	        	}
			log.Printf("GIF to PNG")
		}
	}

	return nil
}
