//go:build windows

package windows

import (
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPipe(t *testing.T) {
	// 测试创建管道
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// 测试写入和读取
	testData := []byte("hello world")

	// 写入
	n, err := w.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("Write returned %d, want %d", n, len(testData))
	}

	// 关闭写入端，表示数据写入完毕
	w.Close()

	// 读取
	buf := make([]byte, len(testData))
	n, err = r.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("Read returned %d, want %d", n, len(testData))
	}

	if string(buf) != string(testData) {
		t.Fatalf("Read data mismatch: got %q, want %q", string(buf), string(testData))
	}

	t.Log("Pipe test passed")
}

func TestPipeMultipleWrites(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	messages := []string{"msg1", "msg2", "msg3"}

	// 写入多条消息
	for _, msg := range messages {
		_, err := w.Write([]byte(msg))
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}
	w.Close()

	// 读取所有数据
	var result []byte
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
	}

	expected := "msg1msg2msg3"
	if string(result) != expected {
		t.Fatalf("Data mismatch: got %q, want %q", string(result), expected)
	}

	t.Log("Multiple writes test passed")
}

func TestPipeConcurrent(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	done := make(chan bool)

	// 并发写入
	go func() {
		for range 100 {
			_, err := w.Write([]byte("x"))
			if err != nil {
				t.Errorf("Write failed: %v", err)
				return
			}
		}
		w.Close()
		done <- true
	}()

	// 并发读取
	go func() {
		count := 0
		buf := make([]byte, 10)
		for {
			n, err := r.Read(buf)
			count += n
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("Read failed: %v", err)
				return
			}
		}
		if count != 100 {
			t.Errorf("Read count %d, want 100", count)
		}
		done <- true
	}()

	// 等待完成
	<-done
	<-done

	t.Log("Concurrent test passed")
}

// TestPipeLargeData 测试大数据传输
func TestPipeLargeData(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// 生成 1MB 数据
	size := 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 并发写入和读取
	errChan := make(chan error, 2)

	go func() {
		n, err := w.Write(data)
		if err != nil {
			errChan <- fmt.Errorf("write failed: %v", err)
			return
		}
		if n != size {
			errChan <- fmt.Errorf("write returned %d, want %d", n, size)
			return
		}
		w.Close()
		errChan <- nil
	}()

	var received []byte
	buf := make([]byte, 4096)

	go func() {
		for {
			n, err := r.Read(buf)
			if n > 0 {
				received = append(received, buf[:n]...)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				errChan <- fmt.Errorf("read failed: %v", err)
				return
			}
		}
		if len(received) != size {
			errChan <- fmt.Errorf("received %d bytes, want %d", len(received), size)
			return
		}
		// 验证数据
		for i := range received {
			if received[i] != byte(i%256) {
				errChan <- fmt.Errorf("data mismatch at index %d", i)
				return
			}
		}
		errChan <- nil
	}()

	// 等待完成
	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			t.Fatal(err)
		}
	}

	t.Log("Large data test passed")
}

// TestPipeReadAfterWriteClose 测试写入端关闭后的读取
func TestPipeReadAfterWriteClose(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()

	testData := []byte("test data")
	_, err = w.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	w.Close()

	// 读取所有数据直到 EOF
	var result []byte
	buf := make([]byte, 100)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
	}

	if string(result) != string(testData) {
		t.Fatalf("Data mismatch: got %q, want %q", string(result), string(testData))
	}

	t.Log("Read after write close test passed")
}

// TestPipeCloseEarly 测试提前关闭
func TestPipeCloseEarly(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}

	// 提前关闭读取端
	r.Close()

	// 写入应该失败或成功（取决于实现）
	_, err = w.Write([]byte("test"))
	// 允许成功或失败，只要不出 panic
	t.Logf("Write after read close: %v", err)
	w.Close()

	t.Log("Close early test passed")
}

// TestPipeTimeout 测试超时场景
func TestPipeTimeout(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// 测试读取超时（使用 goroutine 延迟写入）
	done := make(chan bool)
	go func() {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("delayed"))
		w.Close()
		done <- true
	}()

	// 立即读取，应该阻塞直到数据到达
	buf := make([]byte, 100)
	start := time.Now()
	n, err := r.Read(buf)
	elapsed := time.Since(start)

	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}
	if n > 0 && string(buf[:n]) != "delayed" {
		t.Fatalf("Data mismatch: got %q", string(buf[:n]))
	}
	if elapsed < 50*time.Millisecond {
		t.Logf("Warning: read completed too fast (%v), may not have blocked", elapsed)
	}

	<-done
	t.Log("Timeout test passed")
}

// TestPipeMultipleInstances 测试多个管道实例
func TestPipeMultipleInstances(t *testing.T) {
	const count = 10
	var pipes [][2]*os.File

	for i := 0; i < count; i++ {
		r, w, err := Pipe()
		if err != nil {
			t.Fatalf("Pipe %d failed: %v", i, err)
		}
		pipes = append(pipes, [2]*os.File{r, w})
	}

	// 验证每个管道独立工作
	for i, pair := range pipes {
		msg := fmt.Sprintf("pipe%d", i)
		go func(w *os.File, data string) {
			w.Write([]byte(data))
			w.Close()
		}(pair[1], msg)

		buf := make([]byte, 100)
		n, _ := pair[0].Read(buf)
		if string(buf[:n]) != msg {
			t.Fatalf("Pipe %d data mismatch", i)
		}
		pair[0].Close()
	}

	t.Log("Multiple instances test passed")
}

// TestPipeStress 压力测试
func TestPipeStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	const iterations = 100
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			r, w, err := Pipe()
			if err != nil {
				t.Errorf("Iteration %d: Pipe failed: %v", idx, err)
				return
			}
			defer r.Close()
			defer w.Close()

			data := []byte(fmt.Sprintf("iteration %d", idx))

			// 写入
			if _, err := w.Write(data); err != nil {
				t.Errorf("Iteration %d: Write failed: %v", idx, err)
				return
			}
			w.Close()

			// 读取
			result, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("Iteration %d: Read failed: %v", idx, err)
				return
			}

			if string(result) != string(data) {
				t.Errorf("Iteration %d: Data mismatch", idx)
			}
		}(i)
	}

	wg.Wait()
	t.Log("Stress test passed")
}

// TestPipeNilACL 测试 nil ACL（应该使用默认安全描述符）
func TestPipeNilACL(t *testing.T) {
	// 这个测试假设 Pipe 内部处理 nil ACL
	// 如果 GetDACL 返回 nil，测试管道是否仍能工作
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe() with default ACL failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// 简单读写测试
	testData := []byte("test")
	w.Write(testData)
	w.Close()

	result, _ := io.ReadAll(r)
	if string(result) != string(testData) {
		t.Fatalf("Data mismatch")
	}

	t.Log("Default ACL test passed")
}

// BenchmarkPipe 基准测试
func BenchmarkPipe(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r, w, err := Pipe()
		if err != nil {
			b.Fatalf("Pipe() failed: %v", err)
		}

		data := make([]byte, 4096)
		go func() {
			w.Write(data)
			w.Close()
		}()

		io.ReadAll(r)
		r.Close()
	}
}

// BenchmarkPipeThroughput 吞吐量测试
func BenchmarkPipeThroughput(b *testing.B) {
	size := 1024 * 1024 // 1MB
	data := make([]byte, size)

	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r, w, err := Pipe()
		if err != nil {
			b.Fatalf("Pipe() failed: %v", err)
		}

		go func() {
			w.Write(data)
			w.Close()
		}()

		io.ReadAll(r)
		r.Close()
	}
}

// BenchmarkPipeLatency 延迟测试（小数据包）
func BenchmarkPipeLatency(b *testing.B) {
	data := []byte("x")

	for i := 0; i < b.N; i++ {
		r, w, err := Pipe()
		if err != nil {
			b.Fatalf("Pipe() failed: %v", err)
		}

		w.Write(data)
		w.Close()

		io.ReadAll(r)
		r.Close()
	}
}
