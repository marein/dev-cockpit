package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

// Encrypted backups wrap the tar.gz stream into framed AES-256-GCM, the key
// comes from the password via scrypt. Each frame seals one flag byte (0 more,
// 1 final) plus up to 1 MiB of data under a counter nonce, so export and
// import both stream without holding the whole archive in memory, frames
// cannot be reordered, and a truncated file fails on the missing final frame.

const (
	cryptMagic     = "DCBK1\x00"
	cryptSaltSize  = 16
	cryptChunkSize = 1 << 20
)

var errBadPassword = errors.New("Wrong password or the file is damaged.")

func newGCM(password string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func frameNonce(gcm cipher.AEAD, ctr uint32) []byte {
	nonce := make([]byte, gcm.NonceSize())
	binary.BigEndian.PutUint32(nonce[len(nonce)-4:], ctr)
	return nonce
}

type encryptWriter struct {
	w   io.Writer
	gcm cipher.AEAD
	buf []byte
	n   int
	ctr uint32
	err error
}

// NewEncryptWriter writes the header and returns a writer that encrypts
// everything written to it. Close writes the final frame and must be called.
func NewEncryptWriter(w io.Writer, password string) (io.WriteCloser, error) {
	salt := make([]byte, cryptSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	gcm, err := newGCM(password, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(cryptMagic)); err != nil {
		return nil, err
	}
	if _, err := w.Write(salt); err != nil {
		return nil, err
	}
	return &encryptWriter{w: w, gcm: gcm, buf: make([]byte, cryptChunkSize)}, nil
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	total := len(p)
	for len(p) > 0 {
		n := copy(e.buf[e.n:], p)
		e.n += n
		p = p[n:]
		if e.n == len(e.buf) {
			if err := e.flush(0); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

func (e *encryptWriter) flush(flag byte) error {
	plain := make([]byte, 0, e.n+1)
	plain = append(plain, flag)
	plain = append(plain, e.buf[:e.n]...)
	sealed := e.gcm.Seal(nil, frameNonce(e.gcm, e.ctr), plain, nil)
	e.ctr++
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(sealed)))
	if _, err := e.w.Write(lenBuf[:]); err != nil {
		e.err = err
		return err
	}
	if _, err := e.w.Write(sealed); err != nil {
		e.err = err
		return err
	}
	e.n = 0
	return nil
}

func (e *encryptWriter) Close() error {
	if e.err != nil {
		return e.err
	}
	return e.flush(1)
}

type decryptReader struct {
	r    io.Reader
	gcm  cipher.AEAD
	buf  []byte
	ctr  uint32
	done bool
	err  error
}

// newDecryptReader consumes the header from r and returns a reader yielding
// the decrypted stream. The caller must have checked the magic already.
func newDecryptReader(r io.Reader, password string) (io.Reader, error) {
	header := make([]byte, len(cryptMagic)+cryptSaltSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, errBadPassword
	}
	if string(header[:len(cryptMagic)]) != cryptMagic {
		return nil, errBadPassword
	}
	gcm, err := newGCM(password, header[len(cryptMagic):])
	if err != nil {
		return nil, err
	}
	return &decryptReader{r: r, gcm: gcm}, nil
}

func (d *decryptReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			d.err = err
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func (d *decryptReader) next() error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(d.r, lenBuf[:]); err != nil {
		return errors.New("The encrypted backup is truncated.")
	}
	clen := binary.BigEndian.Uint32(lenBuf[:])
	if clen > cryptChunkSize+64 {
		return errBadPassword
	}
	sealed := make([]byte, clen)
	if _, err := io.ReadFull(d.r, sealed); err != nil {
		return errors.New("The encrypted backup is truncated.")
	}
	plain, err := d.gcm.Open(nil, frameNonce(d.gcm, d.ctr), sealed, nil)
	d.ctr++
	if err != nil || len(plain) == 0 {
		return errBadPassword
	}
	if plain[0] == 1 {
		d.done = true
	}
	d.buf = plain[1:]
	return nil
}
