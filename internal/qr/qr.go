// Package qr builds EMVCo merchant-presented QR payloads for VietQR (NAPAS 247)
// and PayNow (SGQR). The payload is plain text; any QR renderer can draw it.
package qr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrInvalid = errors.New("qr: invalid payment details")

// tlv encodes one EMV data object: 2-digit tag, 2-digit length, value.
func tlv(tag, value string) string {
	return fmt.Sprintf("%s%02d%s", tag, len(value), value)
}

// finish appends the CRC object (tag 63). The checksum covers everything before
// it plus the "6304" tag-and-length prefix, per the EMVCo specification.
func finish(body string) string {
	body += "6304"
	return body + fmt.Sprintf("%04X", CRC16([]byte(body)))
}

// CRC16 is CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF).
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

var (
	reBIN     = regexp.MustCompile(`^[0-9]{6}$`)
	reAccount = regexp.MustCompile(`^[0-9A-Za-z]{4,19}$`)
	reMobile  = regexp.MustCompile(`^\+65[0-9]{8}$`)
	reUEN     = regexp.MustCompile(`^[0-9A-Z]{9,13}$`)
	reAmount  = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{2})?$`)
	reRef     = regexp.MustCompile(`^[0-9A-Za-z ]{0,25}$`)
)

// VietQR builds a NAPAS 247 transfer-to-account payload. amount is in dong
// ("50000"); reference is an optional transfer note.
func VietQR(bankBIN, account, amount, reference string) (string, error) {
	if !reBIN.MatchString(bankBIN) || !reAccount.MatchString(account) ||
		!reAmount.MatchString(amount) || strings.Contains(amount, ".") || !reRef.MatchString(reference) {
		return "", ErrInvalid
	}
	beneficiary := tlv("00", bankBIN) + tlv("01", account)
	merchant := tlv("00", "A000000727") + tlv("01", beneficiary) + tlv("02", "QRIBFTTA")
	b := tlv("00", "01") +
		tlv("01", "12") + // dynamic: amount is fixed
		tlv("38", merchant) +
		tlv("53", "704") +
		tlv("54", amount) +
		tlv("58", "VN")
	if reference != "" {
		b += tlv("62", tlv("08", reference))
	}
	return finish(b), nil
}

// PayNow builds an SGQR PayNow payload. proxyType is "mobile" (+65XXXXXXXX) or
// "uen"; amount is in dollars ("12.50"); banking apps refuse the code after expiry.
func PayNow(proxyType, proxyValue, amount, reference string, expiry time.Time) (string, error) {
	var kind string
	switch proxyType {
	case "mobile":
		if !reMobile.MatchString(proxyValue) {
			return "", ErrInvalid
		}
		kind = "0"
	case "uen":
		if !reUEN.MatchString(proxyValue) {
			return "", ErrInvalid
		}
		kind = "2"
	default:
		return "", ErrInvalid
	}
	if !reAmount.MatchString(amount) || !reRef.MatchString(reference) {
		return "", ErrInvalid
	}
	merchant := tlv("00", "SG.PAYNOW") +
		tlv("01", kind) +
		tlv("02", proxyValue) +
		tlv("03", "0") + // amount not editable by the payer
		tlv("04", expiry.Format("20060102"))
	b := tlv("00", "01") +
		tlv("01", "12") +
		tlv("26", merchant) +
		tlv("52", "0000") +
		tlv("53", "702") +
		tlv("54", amount) +
		tlv("58", "SG") +
		tlv("59", "NA") +
		tlv("60", "Singapore")
	if reference != "" {
		b += tlv("62", tlv("01", reference))
	}
	return finish(b), nil
}

// Parse splits a payload into its top-level objects, verifying structure and CRC.
func Parse(payload string) (map[string]string, error) {
	if len(payload) < 8 {
		return nil, ErrInvalid
	}
	want := fmt.Sprintf("%04X", CRC16([]byte(payload[:len(payload)-4])))
	if payload[len(payload)-8:len(payload)-4] != "6304" || payload[len(payload)-4:] != want {
		return nil, fmt.Errorf("%w: bad checksum", ErrInvalid)
	}
	return ParseTLV(payload)
}

// ParseTLV decodes a flat sequence of EMV objects (also usable on nested templates).
func ParseTLV(s string) (map[string]string, error) {
	out := map[string]string{}
	for len(s) > 0 {
		if len(s) < 4 {
			return nil, ErrInvalid
		}
		var n int
		if _, err := fmt.Sscanf(s[2:4], "%02d", &n); err != nil || len(s) < 4+n {
			return nil, ErrInvalid
		}
		out[s[:2]] = s[4 : 4+n]
		s = s[4+n:]
	}
	return out, nil
}
