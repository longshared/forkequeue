package levelqueue

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/longshared/forkequeue/internal/util"
	"github.com/syndtr/goleveldb/leveldb"
)

type Server struct {
	pendingDB   *leveldb.DB
	checkSameDB *leveldb.DB
	sync.RWMutex

	opts atomic.Value

	topicMap   map[string]*Topic
	exitChan   chan int
	waitGroup  util.WaitGroupWrapper
	isLoading  int32
	isExiting  int32
	notifyChan chan interface{}

	poolSize int

	startTime time.Time
}

func (s *Server) getOpts() *Options {
	return s.opts.Load().(*Options)
}

func (s *Server) storeOpts(opts *Options) {
	s.opts.Store(opts)
}

type meta struct {
	Topics []struct {
		Name string `json:"name"`
	} `json:"topics"`
}

func newMetadataFile(opts *Options) string {
	return path.Join(opts.DataPath, "server-topic.dat")
}

func readOrEmpty(fn string) ([]byte, error) {
	data, err := ioutil.ReadFile(fn)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read metadata from %s - %s", fn, err)
		}
	}
	return data, nil
}

func writeSyncFile(fn string, data []byte) error {
	f, err := os.OpenFile(fn, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	defer f.Close()
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}

	return err
}

func (s *Server) LoadMetadata() error {
	atomic.StoreInt32(&s.isLoading, 1)
	defer atomic.StoreInt32(&s.isLoading, 0)

	fn := newMetadataFile(s.getOpts())

	data, err := readOrEmpty(fn)
	if err != nil {
		return err
	}
	if data == nil {
		return nil // fresh start
	}

	var m meta
	err = json.Unmarshal(data, &m)
	if err != nil {
		return fmt.Errorf("failed to parse metadata in %s - %s", fn, err)
	}

	for _, t := range m.Topics {

		topic := s.GetTopic(t.Name)

		topic.Start()
	}
	return nil
}

func (s *Server) PersistMetadata() error {
	// persist metadata about what topics/channels we have, across restarts
	fileName := newMetadataFile(s.getOpts())

	jsonData := make(map[string]interface{})

	var topics []interface{}
	for _, topic := range s.topicMap {

		topicData := make(map[string]interface{})
		topicData["name"] = topic.name

		topics = append(topics, topicData)
	}

	jsonData["topics"] = topics

	data, err := json.Marshal(&jsonData)
	if err != nil {
		return err
	}

	tmpFileName := fmt.Sprintf("%s.%d.tmp", fileName, rand.Int())

	err = writeSyncFile(tmpFileName, data)
	if err != nil {
		return err
	}
	err = os.Rename(tmpFileName, fileName)
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) GetTopic(topicName string) *Topic {
	s.RLock()
	t, ok := s.topicMap[topicName]
	s.RUnlock()
	if ok {
		return t
	}

	s.Lock()

	t, ok = s.topicMap[topicName]
	if ok {
		s.Unlock()
		return t
	}

	t = NewTopic(topicName, s)
	s.topicMap[topicName] = t

	s.Unlock()

	if atomic.LoadInt32(&s.isLoading) == 1 {
		return t
	}

	t.Start()
	return t
}

func (s *Server) topics() []*Topic {
	var topics []*Topic
	s.RLock()
	for _, t := range s.topicMap {
		topics = append(topics, t)
	}
	s.RUnlock()
	return topics
}

func (s *Server) Notify(v interface{}) {
	loading := atomic.LoadInt32(&s.isLoading) == 1

	s.waitGroup.Wrap(func() {
		select {
		case <-s.exitChan:
		case s.notifyChan <- v:
			if loading {
				<-s.notifyChan
				return
			}
			s.Lock()
			err := s.PersistMetadata()
			if err != nil {
				log.Printf("failed to persist metadata - %s\n", err)
			}
			s.Unlock()
			<-s.notifyChan
		}
	})
}
func (s *Server) WaitGroup() util.WaitGroupWrapper {
	return s.waitGroup
}
func (s *Server) Exit() {
	if !atomic.CompareAndSwapInt32(&s.isExiting, 0, 1) {
		return
	}

	s.Lock()
	err := s.PersistMetadata()
	if err != nil {
		log.Printf("failed to persist metadata - %s\n", err)
	}
	log.Printf("closing topics\n")
	for _, topic := range s.topicMap {
		topic.Close()
	}
	s.Unlock()
	close(s.exitChan)
	s.waitGroup.Wait()
	s.pendingDB.Close()
	s.checkSameDB.Close()
}

func New(opts *Options) *Server {
	s := &Server{
		topicMap:   make(map[string]*Topic),
		exitChan:   make(chan int),
		notifyChan: make(chan interface{}, 1),
		startTime:  time.Now(),
	}
	s.storeOpts(opts)
	return s
}

func (s *Server) InitPendingDB() error {
	filePath := path.Join(s.getOpts().DataPath, "topic-pending-msg")
	db, err := leveldb.OpenFile(filePath, nil)
	if err != nil {
		return err
	}
	s.pendingDB = db
	s.waitGroup.Wrap(s.pendingMsgScanLoop)
	return nil
}

func (s *Server) InitCheckSameDB() error {
	filePath := path.Join(s.getOpts().DataPath, "topic-checksame-msg")
	db, err := leveldb.OpenFile(filePath, nil)
	if err != nil {
		return err
	}
	s.checkSameDB = db
	return nil
}

// resizePool adjusts the size of the pool of pendingScanWorker goroutines
// 1 <= pool <= min(num * 0.25, PendingScanWorkerPoolMax)
func (s *Server) resizePool(num int, workCh chan *Topic, responseCh chan bool, closeCh chan int) {
	poolSize := int(float64(num) * 0.25)
	if poolSize < 1 {
		poolSize = 1
	} else if poolSize > s.getOpts().PendingScanWorkerPoolMax {
		poolSize = s.getOpts().PendingScanWorkerPoolMax
	}

	for {
		if poolSize == s.poolSize {
			break
		} else if poolSize < s.poolSize {
			closeCh <- 1
			s.poolSize--
		} else {
			s.waitGroup.Wrap(func() {
				s.pendingScanWorker(workCh, responseCh, closeCh)
			})
			s.poolSize++
		}
	}

}

func (s *Server) pendingScanWorker(workCh chan *Topic, responseCh chan bool, closeCh chan int) {
	for {
		select {
		case t := <-workCh:
			now := time.Now().UnixNano()
			dirty := false
			if t.processInAckQueue(now) {
				dirty = true
			}
			responseCh <- dirty
		case <-closeCh:
			return
		}
	}
}

func (s *Server) pendingMsgScanLoop() {
	workCh := make(chan *Topic, s.getOpts().PendingScanSelectionCount)
	responseCh := make(chan bool, s.getOpts().PendingScanSelectionCount)
	closeCh := make(chan int)

	workerTicker := time.NewTicker(s.getOpts().PendingScanInterval)
	refreshTicker := time.NewTicker(s.getOpts().PendingScanRefreshInterval)

	topics := s.topics()
	s.resizePool(len(topics), workCh, responseCh, closeCh)

	for {
		select {
		case <-workerTicker.C:
			if len(topics) == 0 {
				continue
			}
		case <-refreshTicker.C:
			topics = s.topics()
			s.resizePool(len(topics), workCh, responseCh, closeCh)
			continue
		case <-s.exitChan:
			goto exit
		}

		num := s.getOpts().PendingScanSelectionCount
		if num > len(topics) {
			num = len(topics)
		}

	loop:
		for _, i := range util.UniqRands(num, len(topics)) {
			workCh <- topics[i]
		}

		numDirty := 0
		for i := 0; i < num; i++ {
			if <-responseCh {
				numDirty++
			}
		}

		if float64(numDirty)/float64(num) > s.getOpts().PendingScanDirtyPercent {
			goto loop
		}
	}

exit:
	log.Println("PendingScan:closing")
	close(closeCh)
	workerTicker.Stop()
	refreshTicker.Stop()
}

func (s *Server) GetStartTime() time.Time {
	return s.startTime
}
func (s *Server) Close() {
	s.checkSameDB.Close()
	s.pendingDB.Close()
	for _, value := range s.topicMap {
		value.queue.ldb.Close()
	}
}

func (s *Server) Clean(topic string) {
	s.checkSameDB.Close()
	s.pendingDB.Close()
	s.pendingDB.Delete([]byte(topic), nil)
	s.checkSameDB.Delete([]byte(topic), nil)
}

//遍历所有key value
func (s *Server) ReadAll() {
	db := s.pendingDB
	iter := db.NewIterator(nil, nil)
	for iter.Next() {
		key := string(iter.Key())
		iter = db.NewIterator(nil, nil)
		for ok := iter.Seek([]byte(key)); ok; ok = iter.Next() {
			fmt.Printf("查找数据:%s, value:%s\n", iter.Key(), iter.Value())
		}

	}
	iter.Release()
}