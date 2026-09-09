package gateway

import "encoding/binary"

// containerSeconds reads duration without decoding or writing audio. Supported: PCM/float WAV
// (validated frame/byte rates and complete data chunks), and FLAC STREAMINFO total samples.
// Other or incomplete containers are unmeasurable and use the host's policy reservation.
func containerSeconds(b []byte) (float64, bool) {
	if len(b) >= 42 && string(b[:4]) == "fLaC" && b[4]&127 == 0 && b[5] == 0 && b[6] == 0 && b[7] == 34 {
		bits := binary.BigEndian.Uint64(b[18:26])
		rate := bits >> 44
		samples := bits & ((1 << 36) - 1)
		if rate > 0 && samples > 0 {
			return float64(samples) / float64(rate), true
		}
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, false
	}
	end := int64(binary.LittleEndian.Uint32(b[4:8])) + 8
	if end > int64(len(b)) || end < 12 {
		return 0, false
	}
	var byteRate, align, data uint64
	for pos := int64(12); pos+8 <= end; {
		size := int64(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		start := pos + 8
		if start+size > end {
			return 0, false
		}
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if size < 16 {
				return 0, false
			}
			f := b[start : start+size]
			format := binary.LittleEndian.Uint16(f[:2])
			channels := uint64(binary.LittleEndian.Uint16(f[2:4]))
			rate := uint64(binary.LittleEndian.Uint32(f[4:8]))
			bits := uint64(binary.LittleEndian.Uint16(f[14:16]))
			align = uint64(binary.LittleEndian.Uint16(f[12:14]))
			byteRate = uint64(binary.LittleEndian.Uint32(f[8:12]))
			if (format != 1 && format != 3) || channels == 0 || rate == 0 || bits == 0 || bits%8 != 0 || align != channels*(bits/8) || byteRate != rate*align {
				return 0, false
			}
		case "data":
			data += uint64(size)
		}
		pos = start + size + (size % 2)
	}
	if byteRate == 0 || align == 0 || data == 0 || data%align != 0 {
		return 0, false
	}
	return float64(data) / float64(byteRate), true
}
