package group

import "testing"

func TestBitmapSetHasCount(t *testing.T) {
	bm := newBitmap(100)
	if got := bm.count(); got != 0 {
		t.Fatalf("空位图 count = %d, want 0", got)
	}
	for _, i := range []int{0, 63, 64, 99} {
		bm.set(i)
	}
	for _, i := range []int{0, 63, 64, 99} {
		if !bm.has(i) {
			t.Errorf("set(%d) 后 has(%d) = false", i, i)
		}
	}
	for _, i := range []int{1, 62, 65, 98} {
		if bm.has(i) {
			t.Errorf("未 set 的 has(%d) = true", i)
		}
	}
	if got := bm.count(); got != 4 {
		t.Errorf("count = %d, want 4", got)
	}
	if got := len(bm.missing(100)); got != 96 {
		t.Errorf("missing 数 = %d, want 96", got)
	}
}

func TestBitmapRoundTrip(t *testing.T) {
	bm := newBitmap(200)
	bm.set(5)
	bm.set(130)
	got := loadBitmap(bm.bytes())
	if !got.has(5) || !got.has(130) {
		t.Fatal("序列化往返后丢失置位")
	}
	if got.count() != 2 {
		t.Fatalf("往返后 count = %d, want 2", got.count())
	}
}

func TestBlockFrameCodec(t *testing.T) {
	data := []byte("hello block")
	payload := encodeBlock(7, data)
	idx, got, err := decodeBlock(payload)
	if err != nil || idx != 7 || string(got) != string(data) {
		t.Fatalf("BLOCK 编解码错误: idx=%d data=%q err=%v", idx, got, err)
	}

	bf := encodeBitfield(50, func() *bitmap {
		b := newBitmap(50)
		b.set(10)
		return b
	}())
	count, bm, err := decodeBitfield(bf)
	if err != nil || count != 50 || !bm.has(10) {
		t.Fatalf("BITFIELD 编解码错误: count=%d err=%v", count, err)
	}
}

func TestMaxActiveSeeds(t *testing.T) {
	cases := map[int]int{1: 0, 2: 1, 4: 2, 5: 3, 6: 3, 10: 3}
	for n, want := range cases {
		if got := maxActiveSeeds(n); got != want {
			t.Errorf("maxActiveSeeds(%d) = %d, want %d", n, got, want)
		}
	}
}
