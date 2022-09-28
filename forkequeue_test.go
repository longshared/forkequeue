package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

func IntToBytes(intNum int) []byte {
	uint16Num := uint16(intNum)
	buf := bytes.NewBuffer([]byte{})
	binary.Write(buf, binary.LittleEndian, uint16Num)
	return buf.Bytes()
}

func TestPush(t *testing.T) {
	Init(t.TempDir())

	data := IntToBytes(100)

	result := Push("test", data)
	assert.Equal(t, result, true)
	Close()
}
func TestPop(b *testing.T) {
	Init(b.TempDir())
	data := IntToBytes(1)
	Push("test", data)
	data1, _ := Pop("test")
	assert.NotEmpty(b, data1)
	Close()
}

func TestPopWithAck(b *testing.T) {
	Init(b.TempDir())
	data := IntToBytes(1)
	Push("test", data)
	var data1 *PopData
	data1, _ = Pop("test")
	ack := FinishAckData{ID: data1.ID}
	assert.Equal(b, true, Ack("test", ack))
	Close()
}
func TestPopZero(b *testing.T) {
	Init(b.TempDir())
	// var data1 *PopData
	var err error
	_, err = Pop("test")
	assert.NotEmpty(b, err)
	Close()
}
func TestStat(b *testing.T) {
	Init(b.TempDir())
	data := IntToBytes(1)
	Push("test", data)
	data1, err := Stats("test")

	b.Logf("TestStat %+v", data1)
	assert.Empty(b, err)
}
