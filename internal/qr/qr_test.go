package qr

import "testing"

func TestCRC16CheckValue(t *testing.T) {
	// Published check value for CRC-16/CCITT-FALSE.
	if got := CRC16([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("CRC16 = %04X, want 29B1", got)
	}
}

func TestVietQR(t *testing.T) {
	p, err := VietQR("970436", "1234567890", "50000", "Chia dinner")
	if err != nil {
		t.Fatal(err)
	}
	top, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if top["00"] != "01" || top["01"] != "12" || top["53"] != "704" || top["54"] != "50000" || top["58"] != "VN" {
		t.Errorf("unexpected top-level fields: %v", top)
	}
	m, _ := ParseTLV(top["38"])
	if m["00"] != "A000000727" || m["02"] != "QRIBFTTA" {
		t.Errorf("unexpected merchant info: %v", m)
	}
	ben, _ := ParseTLV(m["01"])
	if ben["00"] != "970436" || ben["01"] != "1234567890" {
		t.Errorf("unexpected beneficiary: %v", ben)
	}
	add, _ := ParseTLV(top["62"])
	if add["08"] != "Chia dinner" {
		t.Errorf("unexpected reference: %v", add)
	}
}

func TestPayNow(t *testing.T) {
	p, err := PayNow("mobile", "+6591234567", "12.50", "")
	if err != nil {
		t.Fatal(err)
	}
	top, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if top["53"] != "702" || top["54"] != "12.50" || top["58"] != "SG" {
		t.Errorf("unexpected top-level fields: %v", top)
	}
	m, _ := ParseTLV(top["26"])
	if m["00"] != "SG.PAYNOW" || m["01"] != "0" || m["02"] != "+6591234567" || m["03"] != "0" {
		t.Errorf("unexpected merchant info: %v", m)
	}
	if _, ok := top["62"]; ok {
		t.Error("empty reference should be omitted")
	}
}

func TestRejectsBadInput(t *testing.T) {
	if _, err := VietQR("97043", "1234567890", "50000", ""); err == nil {
		t.Error("short BIN accepted")
	}
	if _, err := VietQR("970436", "1234567890", "500.50", ""); err == nil {
		t.Error("fractional dong accepted")
	}
	if _, err := PayNow("mobile", "91234567", "1.00", ""); err == nil {
		t.Error("mobile without country code accepted")
	}
	if _, err := PayNow("nric", "S1234567A", "1.00", ""); err == nil {
		t.Error("unknown proxy type accepted")
	}
}

func TestParseDetectsCorruption(t *testing.T) {
	p, _ := PayNow("uen", "201912345K", "8.00", "INV1")
	bad := []byte(p)
	bad[20] ^= 1
	if _, err := Parse(string(bad)); err == nil {
		t.Error("corrupted payload accepted")
	}
}
