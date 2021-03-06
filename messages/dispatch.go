/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package messages

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/packing/clove/utils"
)

const MaxAsyncMessageProcCount = 1024

type MessageProcFunc = func(*Message) error

type Dispatcher struct {
	fns          map[string]MessageProcFunc
	syncChannel  chan *Message
	asyncCount   int
	asyncTime    int64
	asyncTotal   int64
	asyncTimeMax int64
	asyncTimeMin int64
	mutex        sync.Mutex
	mutexTime    sync.Mutex
}

type MessageObject interface {
	GetMappedTypes() map[int]MessageProcFunc
}

func CreateDispatcher() *Dispatcher {
	sor := new(Dispatcher)
	sor.fns = make(map[string]MessageProcFunc)
	sor.syncChannel = make(chan *Message, 102400)
	sor.asyncCount = 0
	sor.asyncTime = 0
	return sor
}

func (receiver *Dispatcher) recordAsyncTime(tv int64) {
	receiver.mutexTime.Lock()
	defer receiver.mutexTime.Unlock()
	receiver.asyncTime += tv
	receiver.asyncTotal += 1
	if tv > receiver.asyncTimeMax {
		receiver.asyncTimeMax = tv
	}
	if tv < receiver.asyncTimeMin {
		receiver.asyncTimeMin = tv
	} else if receiver.asyncTimeMin == 0 {
		receiver.asyncTimeMin = tv
	}
}

func (receiver *Dispatcher) incAsyncCount() {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	receiver.asyncCount += 1
}

func (receiver *Dispatcher) decAsyncCount() {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	receiver.asyncCount -= 1
}

func (receiver *Dispatcher) GetAsyncCount() int {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	return receiver.asyncCount
}

func (receiver *Dispatcher) GetAsyncInfo() (int64, int64, int64) {
	receiver.mutexTime.Lock()
	defer receiver.mutexTime.Unlock()
	if receiver.asyncTotal > 0 {
		return receiver.asyncTime / receiver.asyncTotal, receiver.asyncTimeMax, receiver.asyncTimeMin
	}
	return 0, receiver.asyncTimeMax, receiver.asyncTimeMin
}

func (receiver *Dispatcher) MessageMapped(scheme, tag, tp int, fn MessageProcFunc) {
	key := fmt.Sprintf("%d-%d-%d", scheme, tag, tp)
	receiver.fns[key] = fn
}

func (receiver *Dispatcher) MessageObjectMapped(scheme, tag int, o MessageObject) {
	fns := o.GetMappedTypes()
	for k, v := range fns {
		receiver.MessageMapped(scheme, tag, k, v)
	}
}

func (receiver *Dispatcher) execMessageProc(message *Message, count bool) {
	for _, tag := range message.messageTag {
		key := fmt.Sprintf("%d-%d-%d", message.messageScheme, tag, message.messageType)
		fn, ok := receiver.fns[key]
		if ok {
			if count {
				receiver.incAsyncCount()
			}
			st := time.Now().UnixNano()
			fn(message)
			st = time.Now().UnixNano() - st
			if count {
				receiver.recordAsyncTime(st)
				receiver.decAsyncCount()
			}
		}
	}
}

func (receiver *Dispatcher) Dispatch() {
	go func() {
		syncMessage, ok := <-receiver.syncChannel
		if ok {
			receiver.execMessageProc(syncMessage, false)
		}
	}()

	go func() {
		for {
			if receiver.GetAsyncCount() >= MaxAsyncMessageProcCount {
				utils.LogInfo("消息分派器阻塞 %d 协程 %d", receiver.GetAsyncCount(), runtime.NumGoroutine())
				time.Sleep(1 * time.Millisecond)
				continue
			}
			message := GlobalMessageQueue.Pop()
			if message == nil {
				utils.LogInfo("消息分派器抛出")
				break
			}

			if message.messageSync {
				go func() {
					receiver.syncChannel <- message
				}()
			} else {
				go func() {
					receiver.execMessageProc(message, true)
				}()
			}
		}
	}()
}

var GlobalDispatcher = CreateDispatcher()
