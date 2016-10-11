package files

import (
	"bytes"
	"crypto/md5" // #nosec
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/cozy/cozy-stack/couchdb"
	"github.com/cozy/cozy-stack/web/jsonapi"
	"github.com/spf13/afero"
)

type fileAttributes struct {
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Size       int64     `json:"size,string"`
	Tags       []string  `json:"tags"`
	MD5Sum     []byte    `json:"md5sum"`
	Executable bool      `json:"executable"`
	Class      string    `json:"class"`
	Mime       string    `json:"mime"`
}

type fileDoc struct {
	QID      string          `json:"_id"`
	FRev     string          `json:"_rev,omitempty"`
	Attrs    *fileAttributes `json:"attributes"`
	FolderID string          `json:"folderID"`
	Path     string          `json:"path"`
}

func (f *fileDoc) ID() string {
	return f.QID
}

func (f *fileDoc) Rev() string {
	return f.FRev
}

func (f *fileDoc) DocType() string {
	return string(FileDocType)
}

func (f *fileDoc) SetID(id string) {
	f.QID = id
}

func (f *fileDoc) SetRev(rev string) {
	f.FRev = rev
}

// implement temporary interface JSONApier
func (f *fileDoc) ToJSONApi() ([]byte, error) {
	qid := f.QID
	data := map[string]interface{}{
		"id":         qid[strings.Index(qid, "/")+1:],
		"type":       f.DocType(),
		"rev":        f.Rev(),
		"attributes": f.Attrs,
	}
	m := map[string]interface{}{
		"data": data,
	}
	return json.Marshal(m)
}

// CreateFileAndUpload is the method for uploading a file onto the filesystem.
func CreateFileAndUpload(m *DocMetadata, fs afero.Fs, dbPrefix string, body io.ReadCloser) (jsonapier jsonapi.JSONApier, err error) {
	if m.Type != FileDocType {
		return errDocTypeInvalid
	}

	pth, _, err := createNewFilePath(m, fs, dbPrefix)
	if err != nil {
		return
	}

	createDate := time.Now()
	attrs := &fileAttributes{
		Name:       m.Name,
		CreatedAt:  createDate,
		UpdatedAt:  createDate,
		Size:       int64(0),
		Tags:       m.Tags,
		MD5Sum:     m.GivenMD5,
		Executable: m.Executable,
		Class:      "document",   // @TODO
		Mime:       "text/plain", // @TODO
	}

	doc := &fileDoc{
		Attrs:    attrs,
		FolderID: m.FolderID,
		Path:     pth,
	}

	// Error handling to make sure the steps of uploading the file and
	// creating the corresponding are both rollbacked in case of an
	// error. This should preserve our VFS coherency a little.
	defer func() {
		if err == nil {
			return
		}
		_, isCouchErr := err.(*couchdb.Error)
		if isCouchErr {
			couchdb.DeleteDoc(dbPrefix, doc)
		} else {
			fs.Remove(pth)
		}
	}()

	if err = copyOnFsAndCheckIntegrity(m, fs, pth, body); err != nil {
		return
	}

	if err = couchdb.CreateDoc(dbPrefix, doc.DocType(), doc); err != nil {
		return
	}

	jsonapier = jsonapi.JSONApier(doc)
	return
}

func copyOnFsAndCheckIntegrity(m *DocMetadata, fs afero.Fs, pth string, r io.ReadCloser) (err error) {
	f, err := fs.Create(pth)
	if err != nil {
		return
	}

	defer f.Close()
	defer r.Close()

	md5H := md5.New() // #nosec
	_, err = io.Copy(f, io.TeeReader(r, md5H))
	if err != nil {
		return err
	}

	calcMD5 := md5H.Sum(nil)
	if !bytes.Equal(m.GivenMD5, calcMD5) {
		return errInvalidHash
	}

	return
}
