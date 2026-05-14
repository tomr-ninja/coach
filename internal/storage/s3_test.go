package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockS3Client struct {
	mock.Mock
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	args := m.Called(ctx, params, optFns)
	if out := args.Get(0); out != nil {
		return out.(*s3.ListObjectsV2Output), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	args := m.Called(ctx, params, optFns)
	if out := args.Get(0); out != nil {
		return out.(*s3.HeadObjectOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	args := m.Called(ctx, params, optFns)
	if out := args.Get(0); out != nil {
		return out.(*s3.GetObjectOutput), args.Error(1)
	}
	return nil, args.Error(1)
}

func sha256Sum(data string) [32]byte {
	return sha256.Sum256([]byte(data))
}

const testBucket = "test-bucket"

func TestParseS3URI(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBucket  string
		wantPrefix  string
		wantErr     bool
		wantErrType error
	}{
		{"simple object", "s3://bucket/key", "bucket", "key", false, nil},
		{"prefix only", "s3://bucket/prefix/", "bucket", "prefix/", false, nil},
		{"bucket only", "s3://bucket", "bucket", "", false, nil},
		{"nested", "s3://bucket/a/b/c", "bucket", "a/b/c", false, nil},
		{"not s3", "http://bucket/key", "", "", true, ErrNotS3URI},
		{"local path", "./data", "", "", true, ErrNotS3URI},
		{"empty", "", "", "", true, ErrNotS3URI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := ParseURI(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrType != nil {
					assert.ErrorIs(t, err, tt.wantErrType)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantPrefix, prefix)
		})
	}
}

func TestParseS3PathOut(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{"bucket and prefix", "bucket/output/abcd", "bucket", "output/abcd", false},
		{"nested prefix", "my-bucket/data/results/fingerprint", "my-bucket", "data/results/fingerprint", false},
		{"no slash", "bucketonly", "", "", true},
		{"empty string", "", "", "", true},
		{"slash only", "/", "", "", false},
		{"slash at end", "bucket/prefix/", "bucket", "prefix/", false},
		{"multiple slashes in prefix", "bkt/a/b/c/d", "bkt", "a/b/c/d", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := ParsePathOut(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantPrefix, prefix)
		})
	}
}

func TestMaybeTransient(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		require.Nil(t, maybeTransient(nil))
	})

	t.Run("network error becomes transient", func(t *testing.T) {
		netErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		err := maybeTransient(netErr)
		require.Error(t, err)
		// net.OpError wraps to our transient Error
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("HTTP 500 becomes transient", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		httpErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{
					StatusCode: 500,
					Request:    req,
				},
			},
			Err: errors.New("internal server error"),
		}
		err := maybeTransient(httpErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "internal server error")
	})

	t.Run("HTTP 502 becomes transient", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{
					StatusCode: 502,
					Request:    req,
				},
			},
			Err: errors.New("bad gateway"),
		}
		err := maybeTransient(respErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad gateway")
	})

	t.Run("HTTP 400 passes through unchanged", func(t *testing.T) {
		original := errors.New("bad request")
		err := maybeTransient(original)
		assert.Equal(t, original, err)
	})

	t.Run("plain error passes through", func(t *testing.T) {
		original := errors.New("some random error")
		err := maybeTransient(original)
		assert.Equal(t, original, err)
	})

	t.Run("DNS error becomes transient", func(t *testing.T) {
		dnsErr := &net.DNSError{Err: "no such host", Name: "example.com"}
		err := maybeTransient(dnsErr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no such host")
	})
}

func TestGetS3ChecksumSHA256(t *testing.T) {
	ctx := context.Background()
	bucket := testBucket
	key := "test/key"

	t.Run("valid checksum", func(t *testing.T) {
		expected := sha256Sum("hello world")
		b64 := base64.StdEncoding.EncodeToString(expected[:])

		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.MatchedBy(func(in *s3.HeadObjectInput) bool {
			return *in.Bucket == bucket && *in.Key == key
		}), mock.Anything).Return(&s3.HeadObjectOutput{
			ChecksumSHA256: aws.String(b64),
		}, nil)

		got, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.NoError(t, err)
		assert.Equal(t, expected, got)
		mockClient.AssertExpectations(t)
	})

	t.Run("nil checksum field returns ErrNoChecksum", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{}, nil)

		_, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoChecksum)
		mockClient.AssertExpectations(t)
	})

	t.Run("empty checksum string returns ErrNoChecksum", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{ChecksumSHA256: aws.String("")}, nil)

		_, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoChecksum)
		mockClient.AssertExpectations(t)
	})

	t.Run("invalid base64 returns ErrNoChecksum", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{ChecksumSHA256: aws.String("!!!not-base64@@@")}, nil)

		_, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoChecksum)
		mockClient.AssertExpectations(t)
	})

	t.Run("wrong length base64 returns ErrNoChecksum", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString([]byte("short"))
		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{ChecksumSHA256: aws.String(short)}, nil)

		_, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoChecksum)
		mockClient.AssertExpectations(t)
	})

	t.Run("HeadObject error propagates", func(t *testing.T) {
		s3Err := errors.New("access denied")
		mockClient := new(mockS3Client)
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(nil, s3Err)

		_, err := getS3ChecksumSHA256(ctx, mockClient, bucket, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")
		mockClient.AssertExpectations(t)
	})
}

func TestDownloadAndHash(t *testing.T) {
	ctx := context.Background()
	bucket := testBucket
	key := "test/key"

	t.Run("rejects object too large by listing size", func(t *testing.T) {
		mockClient := new(mockS3Client)
		_, err := downloadAndHash(ctx, mockClient, bucket, key, maxChecksumDownloadSize+1)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrObjectTooLarge)
		// No GetObject call should be made
		mockClient.AssertNotCalled(t, "GetObject", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("rejects object too large by ContentLength", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(maxChecksumDownloadSize + 1),
				Body:          io.NopCloser(strings.NewReader("x")),
			}, nil)

		_, err := downloadAndHash(ctx, mockClient, bucket, key, 100)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrObjectTooLarge)
		mockClient.AssertExpectations(t)
	})

	t.Run("GetObject error propagates", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(nil, errors.New("no such key"))

		_, err := downloadAndHash(ctx, mockClient, bucket, key, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get object")
		assert.Contains(t, err.Error(), "no such key")
		mockClient.AssertExpectations(t)
	})

	t.Run("successful download and hash", func(t *testing.T) {
		content := "hello, s3!"
		expected := sha256Sum(content)

		mockClient := new(mockS3Client)
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(int64(len(content))),
				Body:          io.NopCloser(strings.NewReader(content)),
			}, nil)

		got, err := downloadAndHash(ctx, mockClient, bucket, key, int64(len(content)))
		require.NoError(t, err)
		assert.Equal(t, expected, got)
		mockClient.AssertExpectations(t)
	})

	t.Run("read body error", func(t *testing.T) {
		mockClient := new(mockS3Client)
		failingReader := io.NopCloser(&errorReader{err: errors.New("broken pipe")})
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(10),
				Body:          failingReader,
			}, nil)

		_, err := downloadAndHash(ctx, mockClient, bucket, key, 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read body")
		mockClient.AssertExpectations(t)
	})
}

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func TestChecksumsWithClient(t *testing.T) {
	ctx := context.Background()
	bucket := testBucket
	prefix := "test-prefix/"

	t.Run("all objects have SHA256 checksum metadata", func(t *testing.T) {
		content := "data"
		chk := sha256Sum(content)
		b64 := base64.StdEncoding.EncodeToString(chk[:])

		mockClient := new(mockS3Client)

		// Single page with 2 objects
		mockClient.On("ListObjectsV2", ctx, mock.MatchedBy(func(in *s3.ListObjectsV2Input) bool {
			return *in.Bucket == bucket && *in.Prefix == prefix
		}), mock.Anything).Return(&s3.ListObjectsV2Output{
			Contents: []s3types.Object{
				{Key: aws.String("test-prefix/a.txt"), Size: aws.Int64(4)},
				{Key: aws.String("test-prefix/b.txt"), Size: aws.Int64(4)},
			},
		}, nil).Once()

		mockClient.On("HeadObject", ctx, mock.MatchedBy(func(in *s3.HeadObjectInput) bool {
			return *in.Key == "test-prefix/a.txt"
		}), mock.Anything).Return(&s3.HeadObjectOutput{
			ChecksumSHA256: aws.String(b64),
		}, nil).Once()

		mockClient.On("HeadObject", ctx, mock.MatchedBy(func(in *s3.HeadObjectInput) bool {
			return *in.Key == "test-prefix/b.txt"
		}), mock.Anything).Return(&s3.HeadObjectOutput{
			ChecksumSHA256: aws.String(b64),
		}, nil).Once()

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 2)
		assert.Equal(t, chk, checksums[0])
		assert.Equal(t, chk, checksums[1])
		mockClient.AssertExpectations(t)
	})

	t.Run("falls back to download when checksum metadata missing", func(t *testing.T) {
		content := "fallback-data"
		chk := sha256Sum(content)

		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/file.txt"), Size: aws.Int64(int64(len(content)))},
				},
			}, nil).Once()

		// First HeadObject: no checksum → ErrNoChecksum
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{}, nil).Once()

		// Falls through to download
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(int64(len(content))),
				Body:          io.NopCloser(strings.NewReader(content)),
			}, nil).Once()

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 1)
		assert.Equal(t, chk, checksums[0])
		mockClient.AssertExpectations(t)
	})

	t.Run("skip directory entries ending with slash", func(t *testing.T) {
		content := "real"
		chk := sha256Sum(content)
		b64 := base64.StdEncoding.EncodeToString(chk[:])

		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/"), Size: aws.Int64(0)},                           // directory → skipped
					{Key: aws.String("test-prefix/real.txt"), Size: aws.Int64(int64(len(content)))}, // real file
				},
			}, nil).Once()

		mockClient.On("HeadObject", ctx, mock.MatchedBy(func(in *s3.HeadObjectInput) bool {
			return *in.Key == "test-prefix/real.txt"
		}), mock.Anything).Return(&s3.HeadObjectOutput{
			ChecksumSHA256: aws.String(b64),
		}, nil).Once()

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 1)
		mockClient.AssertExpectations(t)
	})

	t.Run("empty result returns ErrNoObjects", func(t *testing.T) {
		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{}, nil).Once()

		_, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoObjects)
		assert.Contains(t, err.Error(), "s3://test-bucket/test-prefix/")
		mockClient.AssertExpectations(t)
	})

	t.Run("pagination error", func(t *testing.T) {
		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(nil, errors.New("network timeout")).Once()

		_, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list s3 objects")
		mockClient.AssertExpectations(t)
	})

	t.Run("multiple pages", func(t *testing.T) {
		content := "page"
		chk := sha256Sum(content)
		b64 := base64.StdEncoding.EncodeToString(chk[:])

		mockClient := new(mockS3Client)

		// Page 1
		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/p1.txt"), Size: aws.Int64(int64(len(content)))},
				},
				NextContinuationToken: aws.String("token1"),
				IsTruncated:           aws.Bool(true),
			}, nil).Once()

		// Page 2
		mockClient.On("ListObjectsV2", ctx, mock.MatchedBy(func(in *s3.ListObjectsV2Input) bool {
			return in.ContinuationToken != nil && *in.ContinuationToken == "token1"
		}), mock.Anything).Return(&s3.ListObjectsV2Output{
			Contents: []s3types.Object{
				{Key: aws.String("test-prefix/p2.txt"), Size: aws.Int64(int64(len(content)))},
			},
		}, nil).Once()

		// Both objects have checksums
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{ChecksumSHA256: aws.String(b64)}, nil)

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 2)
		mockClient.AssertExpectations(t)
	})

	t.Run("download error in goroutine", func(t *testing.T) {
		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/bad.txt"), Size: aws.Int64(100)},
				},
			}, nil).Once()

		// HeadObject returns no checksum → fallback to download
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.HeadObjectOutput{}, nil).Once()

		// GetObject fails
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(nil, errors.New("access denied")).Once()

		_, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum")
		assert.Contains(t, err.Error(), "access denied")
		mockClient.AssertExpectations(t)
	})

	t.Run("stops checksum lookups after first ErrNoChecksum", func(t *testing.T) {
		content := "data"
		chk := sha256Sum(content)

		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/first.txt"), Size: aws.Int64(int64(len(content)))},
					{Key: aws.String("test-prefix/second.txt"), Size: aws.Int64(int64(len(content)))},
				},
			}, nil).Once()

		// First object: no checksum → ErrNoChecksum, tryChecksum set to false
		mockClient.On("HeadObject", ctx, mock.MatchedBy(func(in *s3.HeadObjectInput) bool {
			return *in.Key == "test-prefix/first.txt"
		}), mock.Anything).Return(&s3.HeadObjectOutput{}, nil).Once()

		// Second object: should NOT call HeadObject (checksums disabled), goes straight to download
		// First object downloaded
		mockClient.On("GetObject", mock.Anything, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(int64(len(content))),
				Body:          io.NopCloser(strings.NewReader(content)),
			}, nil).Once()

		// Second object downloaded (no HeadObject check needed)
		mockClient.On("GetObject", mock.Anything, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(int64(len(content))),
				Body:          io.NopCloser(strings.NewReader(content)),
			}, nil).Once()

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 2)
		assert.Equal(t, chk, checksums[0])
		assert.Equal(t, chk, checksums[1])

		// HeadObject should only be called once (for the first object)
		mockClient.AssertNumberOfCalls(t, "HeadObject", 1)
		mockClient.AssertNumberOfCalls(t, "GetObject", 2)
	})

	t.Run("transient HeadObject error falls through to download", func(t *testing.T) {
		content := "recovered"
		chk := sha256Sum(content)

		mockClient := new(mockS3Client)

		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("test-prefix/transient.txt"), Size: aws.Int64(int64(len(content)))},
				},
			}, nil).Once()

		// HeadObject transient error → object falls through to download, but tryChecksum stays true
		mockClient.On("HeadObject", ctx, mock.Anything, mock.Anything).
			Return(nil, errors.New("throttled")).Once()

		// Falls through to download
		mockClient.On("GetObject", ctx, mock.Anything, mock.Anything).
			Return(&s3.GetObjectOutput{
				ContentLength: aws.Int64(int64(len(content))),
				Body:          io.NopCloser(strings.NewReader(content)),
			}, nil).Once()

		checksums, err := ChecksumsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.Len(t, checksums, 1)
		assert.Equal(t, chk, checksums[0])
		mockClient.AssertExpectations(t)
	})
}

// ---------------------------------------------------------------------------
// artifactExistsWithClient
// ---------------------------------------------------------------------------

func TestArtifactExistsWithClient(t *testing.T) {
	ctx := context.Background()
	bucket := testBucket
	prefix := "output/"

	t.Run("objects exist", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("ListObjectsV2", ctx, mock.MatchedBy(func(in *s3.ListObjectsV2Input) bool {
			return *in.Bucket == bucket && *in.Prefix == prefix && *in.MaxKeys == int32(1)
		}), mock.Anything).Return(&s3.ListObjectsV2Output{
			Contents: []s3types.Object{
				{Key: aws.String("output/file1.txt")},
			},
		}, nil)

		exists, err := artifactExistsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.True(t, exists)
		mockClient.AssertExpectations(t)
	})

	t.Run("no objects exist", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(&s3.ListObjectsV2Output{}, nil)

		exists, err := artifactExistsWithClient(ctx, mockClient, bucket, prefix)
		require.NoError(t, err)
		assert.False(t, exists)
		mockClient.AssertExpectations(t)
	})

	t.Run("ListObjectsV2 error", func(t *testing.T) {
		mockClient := new(mockS3Client)
		mockClient.On("ListObjectsV2", ctx, mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("network down"))

		_, err := artifactExistsWithClient(ctx, mockClient, bucket, prefix)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list s3 objects")
		assert.Contains(t, err.Error(), "network down")
		mockClient.AssertExpectations(t)
	})
}
