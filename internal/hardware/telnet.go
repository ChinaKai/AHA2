package hardware

type telnetCodec struct {
	pending []byte
	cols    int
	rows    int
}

const (
	telnetIAC  = 255
	telnetDONT = 254
	telnetDO   = 253
	telnetWONT = 252
	telnetWILL = 251
	telnetSB   = 250
	telnetSE   = 240

	telnetBinary = 0
	telnetEcho   = 1
	telnetSGA    = 3
	telnetTTYPE  = 24
	telnetNAWS   = 31
	telnetIS     = 0
	telnetSEND   = 1
)

func newTelnetCodec(cols, rows int) *telnetCodec {
	if cols < 20 {
		cols = 100
	}
	if rows < 8 {
		rows = 28
	}
	return &telnetCodec{cols: cols, rows: rows}
}

func (codec *telnetCodec) InitialNegotiation() []byte {
	result := []byte{
		telnetIAC, telnetWILL, telnetBinary,
		telnetIAC, telnetDO, telnetBinary,
		telnetIAC, telnetWILL, telnetNAWS,
	}
	return append(result, codec.windowSize()...)
}

func (codec *telnetCodec) Encode(data []byte) []byte {
	result := make([]byte, 0, len(data))
	for _, value := range data {
		result = append(result, value)
		if value == telnetIAC {
			result = append(result, telnetIAC)
		}
	}
	return result
}

func (codec *telnetCodec) Resize(cols, rows int) []byte {
	if cols < 20 || rows < 8 {
		return nil
	}
	codec.cols = cols
	codec.rows = rows
	return codec.windowSize()
}

func (codec *telnetCodec) Feed(chunk []byte) ([]byte, []byte) {
	data := append(append([]byte(nil), codec.pending...), chunk...)
	codec.pending = nil
	output := make([]byte, 0, len(data))
	reply := make([]byte, 0, 32)
	for index := 0; index < len(data); {
		if data[index] != telnetIAC {
			output = append(output, data[index])
			index++
			continue
		}
		if index+1 >= len(data) {
			codec.pending = append([]byte(nil), data[index:]...)
			break
		}
		command := data[index+1]
		if command == telnetIAC {
			output = append(output, telnetIAC)
			index += 2
			continue
		}
		if command == telnetSB {
			end := -1
			for cursor := index + 2; cursor+1 < len(data); cursor++ {
				if data[cursor] == telnetIAC && data[cursor+1] == telnetSE {
					end = cursor
					break
				}
			}
			if end < 0 {
				codec.pending = append([]byte(nil), data[index:]...)
				break
			}
			if end > index+3 && data[index+2] == telnetTTYPE && data[index+3] == telnetSEND {
				reply = append(reply, telnetIAC, telnetSB, telnetTTYPE, telnetIS)
				reply = append(reply, []byte("xterm-256color")...)
				reply = append(reply, telnetIAC, telnetSE)
			}
			index = end + 2
			continue
		}
		if index+2 >= len(data) {
			codec.pending = append([]byte(nil), data[index:]...)
			break
		}
		option := data[index+2]
		switch command {
		case telnetDO:
			if option == telnetBinary || option == telnetSGA || option == telnetTTYPE || option == telnetNAWS {
				reply = append(reply, telnetIAC, telnetWILL, option)
				if option == telnetNAWS {
					reply = append(reply, codec.windowSize()...)
				}
			} else {
				reply = append(reply, telnetIAC, telnetWONT, option)
			}
		case telnetWILL:
			if option == telnetBinary || option == telnetEcho || option == telnetSGA {
				reply = append(reply, telnetIAC, telnetDO, option)
			} else {
				reply = append(reply, telnetIAC, telnetDONT, option)
			}
		case telnetDONT:
			reply = append(reply, telnetIAC, telnetWONT, option)
		case telnetWONT:
			reply = append(reply, telnetIAC, telnetDONT, option)
		}
		index += 3
	}
	return output, reply
}

func (codec *telnetCodec) windowSize() []byte {
	payload := []byte{
		byte(codec.cols >> 8), byte(codec.cols),
		byte(codec.rows >> 8), byte(codec.rows),
	}
	return []byte{telnetIAC, telnetSB, telnetNAWS, payload[0], payload[1], payload[2], payload[3], telnetIAC, telnetSE}
}
