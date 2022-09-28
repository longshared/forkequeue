package queue

import (
	"fmt"
	"testing"
)

func TestQueue(t *testing.T) {
	Init("./.queue")
	//data := queue.Push(queueInfo)
	topic := "TestQueue"
	data := []byte("hello world")
	//logs.Debug(string(res))

	Push(topic, data)

	pop, err := Pop(topic)
	if err != nil {
		return
	}
	fmt.Printf("%v,%s\n", pop.ID, pop.Data)
}

func TestClean(t *testing.T) {
	Init("./.queue")
	topic := "TestQueue"

	//for i := 0; i < 10; i++ {
	//	str := "hello world" + strconv.Itoa(i)
	//	data := []byte(str)
	//	//logs.Debug(string(res))
	//
	//	Push(topic, data)
	//}
	server.ReadAll()
	RemoveData(topic)

	stats, err := Stats(topic)
	if err != nil {
		return
	}
	fmt.Printf("%v", stats.Topics)

}
