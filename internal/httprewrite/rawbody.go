package httprewrite

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/hashicorp/go-multierror"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

// StreamRewrite decodes the HTTP response body and passes it to the processor
// for transformation in a streaming fashion.
//
// It automatically handles decompression and character set conversion to UTF-8.
// The processor receives the unpacked (decompressed and decoded) response body as an io.ReadCloser and
// writes the transformed output to the provided io.PipeWriter. The processor
// is responsible for closing both streams.
//
// 2026-11-20 (W2, learn-from-upstream 0cc69c4): the response keeps its own
// charset. Before this change every rewritten response was labelled
// `charset=utf-8` while the bytes of a GBK/Shift_JIS body were re-sent as if
// they were UTF-8, so the browser decoded mojibake and injection markers could
// be dropped; the processor still works on UTF-8 (that is what the injectors
// emit) and the output is re-encoded to the original charset here.
func StreamRewrite(res *http.Response, processor func(src io.ReadCloser, dst *io.PipeWriter)) error {
	raw, err := openRawBody(res)
	if err != nil {
		return fmt.Errorf("get raw body reader: %v", err)
	}

	reader, writer := io.Pipe()
	go processor(raw.reader, writer)

	res.Body = raw.output(reader)
	// Content-Length cannot be determined ahead of time.
	// To get around this, the response is chunked to allow for HTTP connection reuse without having to TCP FIN terminate each connection.
	res.ContentLength = -1
	res.Header.Del("Content-Length")
	raw.stampHeaders(res)
	return nil
}

// BufferRewrite reads and decodes the HTTP response body, applies a transformation
// to it using the provided processor function, and replaces the original body
// with the transformed version.
//
// It automatically handles decompression and character set conversion to UTF-8.
// The processor receives the fully buffered, unpacked (decompressed and decoded) body as input and returns
// a modified byte slice.
func BufferRewrite(res *http.Response, processor func(src []byte) []byte) error {
	raw, err := openRawBody(res)
	if err != nil {
		return fmt.Errorf("get raw body reader: %v", err)
	}
	defer raw.close()

	rawBody, err := io.ReadAll(raw.reader)
	if err != nil {
		return fmt.Errorf("read body: %v", err)
	}

	processedBody, err := raw.reencode(processor(rawBody))
	if err != nil {
		return fmt.Errorf("re-encode body: %v", err)
	}

	res.Body = io.NopCloser(bytes.NewReader(processedBody))
	res.ContentLength = int64(len(processedBody))
	res.Header.Set("Content-Length", fmt.Sprint(len(processedBody)))
	res.TransferEncoding = nil
	raw.stampHeaders(res)
	return nil
}

// BufferRewriteLimited behaves like BufferRewrite but skips the transformation
// when the decoded body is larger than limit bytes: overLimit reports true and
// the response body is passed through unchanged (already decompressed and
// decoded to UTF-8, chunked since the final length is unknown).
//
// Used by $replace, whose documented semantics skip responses larger than
// 10 MB; buffering such bodies whole just to leave them untouched would be
// wasteful. 2026-10-08 (batch B8).
func BufferRewriteLimited(res *http.Response, limit int64, processor func(src []byte) []byte) (overLimit bool, err error) {
	raw, err := openRawBody(res)
	if err != nil {
		return false, fmt.Errorf("get raw body reader: %v", err)
	}

	lr := io.LimitedReader{R: raw.reader, N: limit + 1}
	head, err := io.ReadAll(&lr)
	if err != nil {
		raw.close()
		return false, fmt.Errorf("read body: %v", err)
	}

	if int64(len(head)) > limit {
		// Over the limit: stream the already-buffered head plus the rest of
		// the decoded body through unchanged, re-encoded like any other body.
		// raw.close() closes the decompressor and the original body once the
		// client is done.
		res.Body = raw.output(io.MultiReader(bytes.NewReader(head), raw.reader))
		res.ContentLength = -1
		res.Header.Del("Content-Length")
		raw.stampHeaders(res)
		return true, nil
	}

	processedBody, err := raw.reencode(processor(head))
	raw.close()
	if err != nil {
		return false, fmt.Errorf("re-encode body: %v", err)
	}

	res.Body = io.NopCloser(bytes.NewReader(processedBody))
	res.ContentLength = int64(len(processedBody))
	res.Header.Set("Content-Length", fmt.Sprint(len(processedBody)))
	res.TransferEncoding = nil
	raw.stampHeaders(res)
	return false, nil
}

// rawBody is a response body opened for rewriting: reader yields UTF-8 text for
// the processor, encoder puts the processor's output back into the charset the
// response declared.
type rawBody struct {
	reader io.ReadCloser
	// encode converts UTF-8 back to the original charset; nil when the body is
	// already UTF-8 (or was converted to UTF-8 on purpose, see rewriteCharset).
	encode encoding.Encoding
	// mimeType is the media type without parameters; rewriteCharset is the
	// charset the response must advertise ("" keeps the original parameters).
	mimeType       string
	originalHeader string
	rewriteCharset string
}

// stampHeaders replaces the stale framing headers after a rewrite and restores
// the response's charset claim (2026-11-20 W2: only UTF-16 and sniffed bodies
// are relabelled to UTF-8; everything else keeps what the server sent).
func (rb *rawBody) stampHeaders(res *http.Response) {
	res.Header.Del("Content-Encoding")
	res.TransferEncoding = []string{"chunked"}
	if rb.rewriteCharset != "" {
		res.Header.Set("Content-Type", fmt.Sprintf("%s; charset=%s", rb.mimeType, rb.rewriteCharset))
		return
	}
	res.Header.Set("Content-Type", rb.originalHeader)
}

// reencode converts a processor's UTF-8 output back to the original charset.
func (rb *rawBody) reencode(body []byte) ([]byte, error) {
	if rb.encode == nil {
		return body, nil
	}
	return rb.encode.NewEncoder().Bytes(body)
}

// output wraps the processor's UTF-8 stream so downstream readers receive bytes
// in the charset stampHeaders advertised. The returned body closes the encoded
// stream and the original body.
func (rb *rawBody) output(src io.Reader) io.ReadCloser {
	if rb.encode == nil {
		return struct {
			io.Reader
			io.Closer
		}{src, rb.reader}
	}

	pr, pw := io.Pipe()
	go func() {
		encodedWriter := transform.NewWriter(pw, rb.encode.NewEncoder())
		_, copyErr := io.Copy(encodedWriter, src)
		// Close flushes the encoder's pending bytes before the pipe closes.
		closeErr := encodedWriter.Close()
		if copyErr == nil {
			copyErr = closeErr
		}
		pw.CloseWithError(copyErr)
	}()

	return struct {
		io.Reader
		io.Closer
	}{pr, &outputCloser{pr: pr, raw: rb.reader}}
}

// outputCloser closes both the encoded pipe and the original body.
type outputCloser struct {
	pr  io.Closer
	raw io.Closer
}

func (o *outputCloser) Close() error {
	var errs *multierror.Error
	if err := o.pr.Close(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err := o.raw.Close(); err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs.ErrorOrNil()
}

func (rb *rawBody) close() {
	if rb.reader != nil {
		_ = rb.reader.Close()
	}
}

// openRawBody extracts an uncompressed, UTF-8 decoded body from a potentially
// compressed and non-UTF-8 encoded HTTP response, together with the encoder
// needed to put it back into the charset the response declared.
func openRawBody(res *http.Response) (*rawBody, error) {
	contentEncoding := res.Header.Get("Content-Encoding")
	contentType := res.Header.Get("Content-Type")
	mimeType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("parse content type %q: %v", contentType, err)
	}

	charsetName := strings.ToLower(strings.TrimSpace(params["charset"]))
	if contentEncoding == "" && (charsetName == "" || charsetName == "utf-8") {
		// The body is already UTF-8 encoded and not compressed: nothing to
		// decode and nothing to re-encode.
		return &rawBody{
			reader:         res.Body,
			mimeType:       mimeType,
			originalHeader: contentType,
		}, nil
	}

	decompressedReader, err := decompressReader(res.Body, contentEncoding)
	if err != nil {
		return nil, fmt.Errorf("create decompressed reader for encoding %q: %v", contentEncoding, err)
	}

	rb := &rawBody{
		mimeType:       mimeType,
		originalHeader: contentType,
	}

	decoder, reencoder, charsetLabel := bodyCharsetCodec(charsetName)
	rb.encode = reencoder
	rb.rewriteCharset = charsetLabel

	decodedReader, err := newDecodedReader(decompressedReader, contentType, decoder)
	if err != nil {
		// [2026-10-14 空body解码失败修复] charset 检测器需要预读 body 嗅探
		// 编码；body 为空/立即 EOF（1688 风控对部分会话发"头到 body 死"响应，
		// 2026-10-14 login.1688.com 实弹 6/6）时这里返回 EOF 错误。原实现在此
		// Close decompressedReader——encoding=="" 时它就是 res.Body 本身——
		// 把原始响应 body 关掉了；调用方 HandleResponse 是"记日志继续转发"
		// 语义，随后 writeResp 读已关闭的 body → http2 errClosedResponseBody
		// → panic(ErrAbortHandler) → 整条入站 h2 连接被掐 → 浏览器
		// ERR_HTTP2_PROTOCOL_ERROR。修法：解码失败降级为"原文直通"——保留
		// body 可读（未解码原文交给调用方，注入器在原字节上自行决策），body
		// 永远存活，连接与请求照常完成。
		rb.reader = res.Body
		rb.encode = nil
		rb.rewriteCharset = ""
		return rb, nil
	}

	rb.reader = struct {
		io.Reader
		io.Closer
	}{
		decodedReader,
		&multiCloser{[]io.Closer{decompressedReader, res.Body}},
	}
	return rb, nil
}

// bodyCharsetCodec decides how to decode a body into UTF-8 and whether it must
// be re-encoded or relabelled afterwards (2026-11-20 W2, 0cc69c4 invariants):
//
//   - UTF-16 has no practical byte-wise re-encoding target here, so it is
//     converted to UTF-8 and the response is relabelled to UTF-8;
//   - a declared legacy charset (GBK, Shift_JIS, windows-1251, ...) is decoded
//     for the processor and re-encoded on the way out, keeping the original
//     Content-Type untouched;
//   - a missing or unrecognised charset is sniffed by the decoder and the
//     response is relabelled to UTF-8, because the original claim would be a
//     lie once the body is rewritten.
//
// A nil decoder means "hand the bytes to charset.NewReader and sniff".
func bodyCharsetCodec(charsetName string) (decoder encoding.Encoding, reencoder encoding.Encoding, rewriteCharset string) {
	if charsetName == "" {
		return nil, nil, "utf-8"
	}

	enc, canonical := charset.Lookup(charsetName)
	if enc == nil {
		return nil, nil, "utf-8"
	}
	if canonical == "utf-8" {
		return enc, nil, ""
	}
	if strings.HasPrefix(charsetName, "utf-16") {
		return enc, nil, "utf-8"
	}
	return enc, enc, ""
}

// newDecodedReader decodes a body to UTF-8. A nil decoder sniffs the encoding
// from the body itself (charset.NewReader) rather than trusting the header.
func newDecodedReader(body io.Reader, contentType string, decoder encoding.Encoding) (io.Reader, error) {
	if decoder != nil {
		return transform.NewReader(body, decoder.NewDecoder()), nil
	}
	return charset.NewReader(body, contentType)
}

// decompressReader decompresses a reader using the specified compression algorithm.
// It does not decompress data encoded with multiple algorithms.
func decompressReader(reader io.ReadCloser, compressionAlg string) (io.ReadCloser, error) {
	// Reference: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
	switch strings.ToLower(compressionAlg) {
	case "gzip":
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %v", err)
		}
		return gzipReader, nil
	case "deflate":
		return flate.NewReader(reader), nil
	case "br":
		return io.NopCloser(brotli.NewReader(reader)), nil
	case "zstd":
		zstdReader, err := zstd.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("create zstd reader: %v", err)
		}
		return io.NopCloser(zstdReader), nil
	case "":
		return reader, nil
	default:
		return nil, errors.New("unsupported encoding")
	}
}

// multiCloser wraps multiple io.Closers and ensures they are closed sequentially.
type multiCloser struct {
	closers []io.Closer
}

// Close iterates over each io.Closer and closes it, capturing any errors.
func (m *multiCloser) Close() error {
	var finalErr *multierror.Error
	for _, closer := range m.closers {
		if err := closer.Close(); err != nil {
			finalErr = multierror.Append(finalErr, err)
		}
	}
	return finalErr.ErrorOrNil()
}
