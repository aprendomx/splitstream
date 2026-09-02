package flv

import "errors"

// ErrNotAVCSequenceHeader indica que el tag no es un AVC sequence header.
var ErrNotAVCSequenceHeader = errors.New("no es un AVC sequence header")

// ErrMalformedSPS indica que el SPS no se pudo decodificar.
var ErrMalformedSPS = errors.New("SPS malformado")

// ParseResolution extrae el ancho y el alto del SPS que viaja dentro de un AVC sequence
// header.
//
// Se prefiere al onMetaData porque este es declarativo y puede mentir: OBS lo suele mandar
// bien, pero nada obliga a que coincida con lo que realmente codifica (spec §3.8).
func ParseResolution(tag []byte) (int, int, error) {
	sps, err := extractSPS(tag)
	if err != nil {
		return 0, 0, err
	}
	return parseSPS(sps)
}

// extractSPS saca el primer SPS del AVCDecoderConfigurationRecord.
//
// Disposición del tag: 1 byte de frameType|codecID, 1 de AVCPacketType, 3 de composition
// time, y luego el record: 1 de versión, 3 de perfil/compat/nivel, 1 con lengthSizeMinusOne,
// 1 con numOfSPS en sus 5 bits bajos, 2 con la longitud del SPS, y el SPS.
func extractSPS(tag []byte) ([]byte, error) {
	const headerLen = 5 // frameType|codecID + AVCPacketType + composition time

	if len(tag) < headerLen {
		return nil, ErrNotAVCSequenceHeader
	}
	if tag[0]&0x80 != 0 || tag[0]&0x0f != CodecIDAVC {
		return nil, ErrNotAVCSequenceHeader
	}
	if tag[1] != 0x00 {
		return nil, ErrNotAVCSequenceHeader
	}

	record := tag[headerLen:]
	// versión + 3 de perfil + lengthSize + numOfSPS + 2 de longitud = 8 como mínimo.
	if len(record) < 8 {
		return nil, ErrMalformedSPS
	}
	if record[5]&0x1f == 0 {
		return nil, ErrMalformedSPS
	}

	spsLen := int(record[6])<<8 | int(record[7])
	if spsLen == 0 || len(record) < 8+spsLen {
		return nil, ErrMalformedSPS
	}
	return record[8 : 8+spsLen], nil
}

// bitReader lee bits de izquierda a derecha, que es como se codifica H.264.
type bitReader struct {
	data []byte
	pos  int // posición en bits
}

func (r *bitReader) bit() (uint, error) {
	if r.pos >= len(r.data)*8 {
		return 0, ErrMalformedSPS
	}
	b := r.data[r.pos/8]
	shift := 7 - uint(r.pos%8)
	r.pos++
	return uint(b>>shift) & 1, nil
}

func (r *bitReader) bits(n int) (uint, error) {
	var out uint
	for i := 0; i < n; i++ {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		out = out<<1 | b
	}
	return out, nil
}

// ue lee un entero sin signo en código exp-Golomb, que es como H.264 codifica casi todos
// sus campos: N ceros, un uno, y luego N bits de resto.
func (r *bitReader) ue() (uint, error) {
	zeros := 0
	for {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		zeros++
		// Un prefijo de más de 32 ceros no es un valor legítimo: es basura o un bucle.
		if zeros > 32 {
			return 0, ErrMalformedSPS
		}
	}
	if zeros == 0 {
		return 0, nil
	}
	rest, err := r.bits(zeros)
	if err != nil {
		return 0, err
	}
	return (1 << uint(zeros)) - 1 + rest, nil
}

// se lee un entero con signo en código exp-Golomb.
func (r *bitReader) se() (int, error) {
	v, err := r.ue()
	if err != nil {
		return 0, err
	}
	if v%2 == 0 {
		return -int(v / 2), nil
	}
	return int((v + 1) / 2), nil
}

// removeEmulationPrevention quita los bytes 0x03 que H.264 inserta para que no aparezcan
// secuencias 0x000001 dentro del payload. Sin quitarlos, el lector de bits se desalinea.
func removeEmulationPrevention(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i >= 2 && i+1 <= len(b) && b[i] == 0x03 && b[i-1] == 0x00 && b[i-2] == 0x00 {
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// parseSPS decodifica el SPS hasta los campos que dan la resolución.
func parseSPS(sps []byte) (int, int, error) {
	if len(sps) < 4 {
		return 0, 0, ErrMalformedSPS
	}

	// sps[0] es la cabecera NAL; el resto es el RBSP.
	r := &bitReader{data: removeEmulationPrevention(sps[1:])}

	profileIDC, err := r.bits(8)
	if err != nil {
		return 0, 0, err
	}
	constraintFlags, err := r.bits(8) // constraint flags + reserved_zero_2bits
	if err != nil {
		return 0, 0, err
	}
	// El estándar (§7.4.2.1.1) exige que reserved_zero_2bits sea 0. Un SPS real jamás
	// los trae a 1; comprobarlo descarta basura que de otro modo produce una
	// resolución con pinta de válida (p. ej. una cadena de bits en 1 que hace que
	// todos los exp-Golomb salgan en 0).
	if constraintFlags&0x03 != 0 {
		return 0, 0, ErrMalformedSPS
	}
	if _, err := r.bits(8); err != nil { // level_idc
		return 0, 0, err
	}
	if _, err := r.ue(); err != nil { // seq_parameter_set_id
		return 0, 0, err
	}

	chromaFormatIDC := uint(1) // 4:2:0 por defecto
	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chromaFormatIDC, err = r.ue()
		if err != nil {
			return 0, 0, err
		}
		if chromaFormatIDC == 3 {
			if _, err := r.bit(); err != nil { // separate_colour_plane_flag
				return 0, 0, err
			}
		}
		if _, err := r.ue(); err != nil { // bit_depth_luma_minus8
			return 0, 0, err
		}
		if _, err := r.ue(); err != nil { // bit_depth_chroma_minus8
			return 0, 0, err
		}
		if _, err := r.bit(); err != nil { // qpprime_y_zero_transform_bypass_flag
			return 0, 0, err
		}
		seqScalingMatrix, err := r.bit()
		if err != nil {
			return 0, 0, err
		}
		if seqScalingMatrix == 1 {
			lists := 8
			if chromaFormatIDC == 3 {
				lists = 12
			}
			for i := 0; i < lists; i++ {
				present, err := r.bit()
				if err != nil {
					return 0, 0, err
				}
				if present == 0 {
					continue
				}
				size := 16
				if i >= 6 {
					size = 64
				}
				last, next := 8, 8
				for j := 0; j < size; j++ {
					if next != 0 {
						delta, err := r.se()
						if err != nil {
							return 0, 0, err
						}
						next = (last + delta + 256) % 256
					}
					if next != 0 {
						last = next
					}
				}
			}
		}
	}

	if _, err := r.ue(); err != nil { // log2_max_frame_num_minus4
		return 0, 0, err
	}
	picOrderCntType, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	switch picOrderCntType {
	case 0:
		if _, err := r.ue(); err != nil { // log2_max_pic_order_cnt_lsb_minus4
			return 0, 0, err
		}
	case 1:
		if _, err := r.bit(); err != nil { // delta_pic_order_always_zero_flag
			return 0, 0, err
		}
		if _, err := r.se(); err != nil { // offset_for_non_ref_pic
			return 0, 0, err
		}
		if _, err := r.se(); err != nil { // offset_for_top_to_bottom_field
			return 0, 0, err
		}
		n, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		if n > 256 {
			return 0, 0, ErrMalformedSPS
		}
		for i := uint(0); i < n; i++ {
			if _, err := r.se(); err != nil {
				return 0, 0, err
			}
		}
	}

	if _, err := r.ue(); err != nil { // max_num_ref_frames
		return 0, 0, err
	}
	if _, err := r.bit(); err != nil { // gaps_in_frame_num_value_allowed_flag
		return 0, 0, err
	}

	widthInMbsMinus1, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	heightInMapUnitsMinus1, err := r.ue()
	if err != nil {
		return 0, 0, err
	}
	frameMbsOnly, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	if frameMbsOnly == 0 {
		if _, err := r.bit(); err != nil { // mb_adaptive_frame_field_flag
			return 0, 0, err
		}
	}
	if _, err := r.bit(); err != nil { // direct_8x8_inference_flag
		return 0, 0, err
	}

	width := int(widthInMbsMinus1+1) * 16
	height := int(heightInMapUnitsMinus1+1) * 16
	if frameMbsOnly == 0 {
		height *= 2
	}

	// El recorte quita las filas y columnas que se codificaron solo para completar
	// macrobloques de 16x16: sin él, 1080 se lee como 1088.
	cropping, err := r.bit()
	if err != nil {
		return 0, 0, err
	}
	if cropping == 1 {
		left, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		right, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		top, err := r.ue()
		if err != nil {
			return 0, 0, err
		}
		bottom, err := r.ue()
		if err != nil {
			return 0, 0, err
		}

		subWidth, subHeight := 2, 2
		switch chromaFormatIDC {
		case 0: // monocromo
			subWidth, subHeight = 1, 1
		case 2: // 4:2:2
			subHeight = 1
		case 3: // 4:4:4
			subWidth, subHeight = 1, 1
		}
		if frameMbsOnly == 0 {
			subHeight *= 2
		}

		width -= int(left+right) * subWidth
		height -= int(top+bottom) * subHeight
	}

	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return 0, 0, ErrMalformedSPS
	}
	return width, height, nil
}
