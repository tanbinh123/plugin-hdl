package hdl

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http"
	"regexp"
	"time"

	. "github.com/Monibuca/engine/v4"
	"github.com/Monibuca/engine/v4/codec"
	"github.com/Monibuca/engine/v4/config"
	"github.com/Monibuca/engine/v4/util"
	. "github.com/logrusorgru/aurora"
	amf "github.com/zhangpeihao/goamf"
)

type HDLConfig struct {
	config.HTTP
	config.Publish
	config.Subscribe
	config.Pull
}

var streamPathReg = regexp.MustCompile(`/(hdl/)?((.+)(\.flv)|(.+))`)

func (c *HDLConfig) Update(override config.Config) {
	if c.ListenAddr != "" || c.ListenAddrTLS != "" {
		plugin.Info(Green("HDL Listen at "), BrightBlue(c.ListenAddr), BrightBlue(c.ListenAddrTLS))
		c.Listen(plugin, c)
	}
}
func (c *HDLConfig) API_Pull(rw http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("target")
	streamPath := r.URL.Query().Get("streamPath")
	if c.PullStream(streamPath, Puller{RemoteURL: targetURL, Config: &c.Pull}) {
		if r.URL.Query().Get("save") != "" {
			c.AddPull(streamPath, targetURL)
			plugin.Modified["pull"] = c.Pull
			if err := plugin.Save(); err != nil {
				plugin.Error(err)
			}
		}
	}
}
func (*HDLConfig) API_List(rw http.ResponseWriter, r *http.Request) {
	util.ReturnJson(FilterStreams[*HDLPuller], time.Second, rw, r)
}

var Config = new(HDLConfig)
var plugin = InstallPlugin(Config)

func (*HDLConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := streamPathReg.FindStringSubmatch(r.RequestURI)
	if len(parts) == 0 {
		w.WriteHeader(404)
		return
	}
	stringPath := parts[3]
	if stringPath == "" {
		stringPath = parts[5]
	}
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Content-Type", "video/x-flv")
	sub := Subscriber{ID: r.RemoteAddr, Type: "FLV"}
	if sub.Subscribe(stringPath, Config.Subscribe) {
		vt, at := sub.WaitVideoTrack(), sub.WaitAudioTrack()
		hasVideo := vt != nil
		hasAudio := at != nil
		var buffer bytes.Buffer
		if _, err := amf.WriteString(&buffer, "onMetaData"); err != nil {
			return
		}
		metaData := amf.Object{
			"MetaDataCreator": "m7s" + Engine.Version,
			"hasVideo":        hasVideo,
			"hasAudio":        hasAudio,
			"hasMatadata":     true,
			"canSeekToEnd":    false,
			"duration":        0,
			"hasKeyFrames":    0,
			"framerate":       0,
			"videodatarate":   0,
			"filesize":        0,
		}
		if _, err := WriteEcmaArray(&buffer, metaData); err != nil {
			return
		}
		var flags byte
		if hasAudio {
			flags |= (1 << 2)
		}
		if hasVideo {
			flags |= 1
		}
		w.Write([]byte{'F', 'L', 'V', 0x01, flags, 0, 0, 0, 9, 0, 0, 0, 0})
		if hasVideo {
			metaData["videocodecid"] = int(vt.CodecID)
			metaData["width"] = vt.SPSInfo.Width
			metaData["height"] = vt.SPSInfo.Height
			sub.OnVideo = func(frame *VideoFrame) error {
				frame.FLV.WriteTo(w)
				return r.Context().Err()
			}
		}
		if hasVideo {
			metaData["audiocodecid"] = int(at.CodecID)
			metaData["audiosamplerate"] = at.SampleRate
			metaData["audiosamplesize"] = at.SampleSize
			metaData["stereo"] = at.Channels == 2
			sub.OnAudio = func(frame *AudioFrame) error {
				frame.FLV.WriteTo(w)
				return r.Context().Err()
			}
		}
		codec.WriteFLVTag(w, codec.FLV_TAG_TYPE_SCRIPT, 0, net.Buffers{buffer.Bytes()})
		if hasVideo {
			vt.DecoderConfiguration.FLV.WriteTo(w)
		}
		if hasAudio && at.CodecID == codec.CodecID_AAC {
			at.DecoderConfiguration.FLV.WriteTo(w)
		}
		sub.Play(at, vt)
	} else {
		w.WriteHeader(500)
	}
}
func WriteEcmaArray(w amf.Writer, o amf.Object) (n int, err error) {
	n, err = amf.WriteMarker(w, amf.AMF0_ECMA_ARRAY_MARKER)
	if err != nil {
		return
	}
	length := int32(len(o))
	err = binary.Write(w, binary.BigEndian, &length)
	if err != nil {
		return
	}
	n += 4
	m := 0
	for name, value := range o {
		m, err = amf.WriteObjectName(w, name)
		if err != nil {
			return
		}
		n += m
		m, err = amf.WriteValue(w, value)
		if err != nil {
			return
		}
		n += m
	}
	m, err = amf.WriteObjectEndMarker(w)
	return n + m, err
}
