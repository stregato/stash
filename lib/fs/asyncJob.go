package fs

import (
	"sync"
	"time"

	"github.com/stregato/mio/lib/core"
	"github.com/stregato/mio/lib/sqlx"
)

var triggerAsync = make(chan string, 16)
var uploadLock sync.Mutex
var activeUploads = make(map[*FS]*time.Timer)

func (fs *FS) HasPutCompleted(id string) bool {
	err := fs.S.DB.QueryRow("GET_FILE_ASYNC", sqlx.Args{"id": id, "safeID": fs.S.ID})
	return err == sqlx.ErrNoRows
}

func (fs *FS) startUploadJob() {
	uploadLock.Lock()
	timer := time.NewTimer(time.Duration(5) * time.Second)
	activeUploads[fs] = timer
	uploadLock.Unlock()
	for {
		var cleanup []string
		select {
		case _, ok := <-timer.C:
			if !ok {
				delete(activeUploads, fs)
				return
			}
			uploadLock.Lock()

			rows, err := fs.S.DB.Query("GET_FILES_ASYNC", sqlx.Args{"safeID": fs.S.ID})
			if err != nil {
				core.Info("cannot get files async: %v", err)
				continue
			}

			for rows.Next() {
				var (
					id        uint64
					file      File
					data      []byte
					deleteSrc bool
					localCopy string
					operation string
				)
				err := rows.Scan(&id, &file, &data, &deleteSrc, &localCopy, &operation)
				if err != nil {
					core.Info("cannot scan file async: %v", err)
					continue
				}
				switch operation {
				case "put":
					err = fs.putSync(file, localCopy, data, deleteSrc)
				case "get":
					err = fs.getSync(file, localCopy, nil)
				}
				if err != nil {
					core.Info("cannot put file async: %v", err)
					continue
				}
				cleanup = append(cleanup, file.ID)
			}
			rows.Close()
		case id := <-triggerAsync:
			var (
				file      File
				data      []byte
				deleteSrc bool
				localCopy string
				operation string
			)
			uploadLock.Lock()
			err := fs.S.DB.QueryRow("GET_FILE_ASYNC", sqlx.Args{"id": id, "safeID": fs.S.ID},
				&file, &data, &deleteSrc, &localCopy, &operation)
			if err != nil {
				core.Info("cannot get file async: %v", err)
				continue
			}
			switch operation {
			case "put":
				err = fs.putSync(file, localCopy, data, deleteSrc)
			case "get":
				err = fs.getSync(file, localCopy, nil)
			}
			if err != nil {
				core.Info("cannot put file async: %v", err)
				continue
			}
			cleanup = append(cleanup, file.ID)
		}

		for _, id := range cleanup {
			_, err := fs.S.DB.Exec("DEL_FILE_ASYNC", sqlx.Args{"id": id, "safeID": fs.S.ID})
			if err != nil {
				core.Info("cannot delete file async: %v", err)
				continue
			}
		}
		uploadLock.Unlock()
	}
}

func (fs *FS) stopUploadJob() {
	uploadLock.Lock()
	timer, ok := activeUploads[fs]
	if ok {
		timer.Stop()
		delete(activeUploads, fs)
	}
	uploadLock.Unlock()
}
