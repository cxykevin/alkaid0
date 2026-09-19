package run

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// ---- 回归测试：后台命令输出捕获必须有上限 ----
//
// 背景：输出此前写进无上限的 bytes.Buffer，一条 `yes` / `cat 大文件` /
// 死循环打印的命令就能把服务端内存吃光（后台任务还可能长期运行）。

func TestCappedBuffer_NoTruncationWhenSmall(t *testing.T) {
	var c cappedBuffer
	if _, err := c.WriteString("hello "); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if got := c.String(); got != "hello world" {
		t.Errorf("small output must be preserved verbatim, got %q", got)
	}
	if c.Dropped() != 0 {
		t.Errorf("no bytes should be dropped, got %d", c.Dropped())
	}
	if strings.Contains(c.String(), "省略") {
		t.Error("small output must not carry a truncation marker")
	}
}

func TestCappedBuffer_BoundsTotalSize(t *testing.T) {
	var c cappedBuffer
	chunk := bytes.Repeat([]byte("x"), 32<<10) // 32 KiB
	total := 0
	for i := 0; i < 400; i++ { // 12.5 MiB
		n, err := c.Write(chunk)
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}

	out := c.Bytes()
	limit := maxCaptureHead + maxCaptureTail + 256 // 标注本身占一点空间
	if len(out) > limit {
		t.Fatalf("captured output must stay bounded: got %d bytes, limit ~%d", len(out), limit)
	}
	if c.Dropped() == 0 {
		t.Fatal("expected dropped bytes to be accounted")
	}
	if int64(len(out))+c.Dropped() < int64(total) {
		t.Errorf("kept(%d) + dropped(%d) must account for all written bytes(%d)", len(out), c.Dropped(), total)
	}
	if !strings.Contains(string(out), "省略") {
		t.Error("truncated output must carry a visible marker")
	}
}

func TestCappedBuffer_KeepsHeadAndNewestTail(t *testing.T) {
	var c cappedBuffer

	// 头部：一段可识别的起始内容
	head := "HEAD-MARKER\n"
	if _, err := c.WriteString(head); err != nil {
		t.Fatal(err)
	}
	// 中间：大量可丢弃内容
	filler := bytes.Repeat([]byte("f"), 4<<20)
	if _, err := c.Write(filler); err != nil {
		t.Fatal(err)
	}
	// 尾部：最后写入的内容必须保留（实时快照依赖它）
	if _, err := c.WriteString("TAIL-MARKER\n"); err != nil {
		t.Fatal(err)
	}

	out := c.String()
	if !strings.Contains(out, "HEAD-MARKER") {
		t.Error("head must be retained")
	}
	if !strings.Contains(out, "TAIL-MARKER") {
		t.Error("newest output must be retained (live snapshots depend on it)")
	}
	if !strings.HasSuffix(out, "TAIL-MARKER\n") {
		t.Error("output must end with the most recent bytes")
	}
	// 实时快照走 tailLines，最新的行必须可见
	if got := tailLines(c.Bytes(), 2); !strings.Contains(got, "TAIL-MARKER") {
		t.Errorf("tailLines must see the newest line, got %q", got)
	}
}

func TestCappedBuffer_SingleHugeWrite(t *testing.T) {
	var c cappedBuffer
	huge := bytes.Repeat([]byte("z"), 8<<20) // 单次 8 MiB 写入
	if _, err := c.Write(huge); err != nil {
		t.Fatal(err)
	}
	if got := len(c.Bytes()); got > maxCaptureHead+maxCaptureTail+256 {
		t.Fatalf("a single huge write must still be bounded, got %d", got)
	}
	if c.Dropped() == 0 {
		t.Error("expected dropped bytes")
	}
}

func TestCappedBuffer_ConcurrentWrites(t *testing.T) {
	var c cappedBuffer
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_, _ = c.WriteString("concurrent line\n")
			}
		}()
	}
	wg.Wait()
	if c.String() == "" {
		t.Error("concurrent writes must still capture output")
	}
}
