package main

import (
	"fmt"
	"testing"
)

func TestMains(t *testing.T) {
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
