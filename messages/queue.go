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
	"github.com/packing/clove/codecs"
	"github.com/packing/clove/nnet"
)

type MessageQueue chan *Message

func (receiver MessageQueue) Push(controller nnet.Controller, addr string, data codecs.IMData) error {
	msg, err := MessageFromData(controller, addr, data)
	if err != nil {
		return err
	}
	var ch = receiver
	go func() {
		ch <- msg
	}()
	return nil
}

func (receiver MessageQueue) Pop() *Message {
	msg, ok := <-receiver
	if !ok {
		return nil
	}
	return msg
}

var GlobalMessageQueue = make(MessageQueue, 102400)
