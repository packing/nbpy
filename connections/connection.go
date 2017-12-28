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

package connections

import (
	"net"
	"bytes"
	"nbpy/codecs"
	"nbpy/errors"
	"nbpy/utils"
	"nbpy/packets"
	"nbpy/env"
)

type ReceiveAction struct {
	err error
	data *bytes.Buffer
}

type Connection struct {
	Handle net.Conn
	Id int
	Encrypt bool
	Compress bool
	OnReceive func(peer Connection, msg codecs.IMData) error
	recvch chan ReceiveAction
	protocol uint32
	codec *codecs.Codec
	packetformat *packets.PacketFormat
}

func (receiver *Connection) SetPacketFormat(packetformat *packets.PacketFormat) {
	utils.LogVerbose(">>> 设定封包格式为 %s (0x%X)", packetformat.Tag, receiver.Id)
	receiver.packetformat = packetformat
}

func (receiver *Connection) SetProtocol(tp, ver uint16) {
	utils.LogVerbose(">>> 设定协议类型和版本为 %d(%d) (0x%X)", tp, ver, receiver.Id)
	//寻找解码器
	err, codec := env.FindCodec(tp, ver)
	if err != nil {
		return
	}
	receiver.codec = codec
	receiver.protocol = uint32(tp) << 16 | uint32(ver)
}

func (receiver Connection) Welcome() {
	utils.LogVerbose(">>> 建立客户端连接 id: 0x%X addr: %s", receiver.Id, receiver.Handle.RemoteAddr().String())
}

func (receiver Connection) Bye() {
	utils.LogVerbose(">>> 关闭客户端连接 id: 0x%X addr: %s", receiver.Id, receiver.Handle.RemoteAddr().String())
}

func (receiver Connection) Process() error{
	utils.LogVerbose(">>> 进入数据处理协程 (0x%X)", receiver.Id)

	//定义起始状态标记
	bProcessed := false

	//如果没有设定数据到达回调，则直接退出处理
	if receiver.OnReceive == nil {
		utils.LogWarn("此连接没有设置数据到达回调函数, 将会被强行关闭")
		return errors.Errorf("OnReceive is not exists")
	}

	for {
		action := <- receiver.recvch

		if receiver.packetformat == nil {
			//如果没有指定封包格式，则进行封包格式选定操作
			err, pf := env.MatchPacketFormat(action.data)
			if err != nil {
				if err == packets.ErrorDataNotMatch {
					//未能匹配任何封包格式，将会中断该连接
					utils.LogWarn("该连接未能匹配到任何通信封包协议, 将会被强行关闭")
					goto exitLabel
				} else {
					//可能数据不足，继续接收事件以等待数据完整
					continue
				}
			}
			utils.LogInfo("成功匹配到通信封包协议为 [%s]", pf.Tag)
			receiver.packetformat = pf
		}

		if !bProcessed {
			bProcessed = true
			//如果仍处于起始状态，调用封包解包器的预处理方法
			err, sd := receiver.packetformat.Parser.Prepare(action.data)
			if err == nil && sd != nil {
				//有待发送数据，直接发送
				receiver.Handle.Write(sd)
			}
			if action.data.Len() == 0 {
				//如果数据已经读完, 等待后续数据到达
				continue
			}
		}

		for {
			err, packet := receiver.packetformat.Parser.Pop(action.data)
			if err != nil {
				if err != packets.ErrorDataNotReady {
					utils.LogError(err.Error())
					goto exitLabel
				}
				break
			}

			if packet == nil {
				break
			}

			if receiver.protocol == 0 {
				//如果当前连接未确定通信协议，根据当前封包属性决定通信协议类型和版本
				receiver.SetProtocol(packet.ProtocolType, packet.ProtocolVer)
			}

			packetData := packet.Raw
			//如果是直接内存流数据协议，则直接转出至回调
			if packet.ProtocolType == codecs.ProtocolMemory {
				err := receiver.OnReceive(receiver, packetData)
				if err != nil {
					utils.LogError(err.Error())
					goto exitLabel
				}
				continue
			}

			if receiver.codec == nil {
				//如果并不是直接内存数据流，而编解码器又未能就绪，则直接中断该连接
				utils.LogError("该连接编解码器未能就绪, 将会被强行关闭")
				goto exitLabel
			}

		readLabel:
			//开始使用解码器进行消息解码(单个封包允许包含多个消息体，所以此处有label供goto回流继续解码下一块消息体)
			err, msg, remianData := receiver.codec.Decoder.Decode(packetData)
			if err == nil {
				err := receiver.OnReceive(receiver, msg)
				if err != nil {
					break
				}
				packetData = remianData
				goto readLabel
			}
		}

		//如果该接收消息带有error信息，则终止处理退出数据处理协程
		if action.err != nil{
			break
		}
	}

	exitLabel:
		utils.LogVerbose("<<< 退出数据处理协程 (0x%X)", receiver.Id)
	return nil
}

func (receiver Connection) Lookup(interval int) error{
	utils.LogVerbose(">>> 进入通信处理协程 (0x%X)", receiver.Id)

	//定义接收缓冲区
	var recvbuffer bytes.Buffer

	//创建与数据处理协程通信的channel
	receiver.recvch = make(chan ReceiveAction)

	//创建数据处理协程
	go receiver.Process()

	for {
		var b = make([]byte, 1024)
		n, err := receiver.Handle.Read(b)
		if n > 0 {
			recvbuffer.Write(b[:n])
			receiver.recvch <- ReceiveAction{err, &recvbuffer}
		}

		if err != nil {
			receiver.recvch <- ReceiveAction{err, &recvbuffer}
			return errors.Errorf("Error at fd.Read.")
		}
	}
	utils.LogVerbose("<<< 退出通信处理协程 (0x%X)", receiver.Id)
	return nil
}

func (receiver Connection) Send(msgs ...codecs.IMData) (error) {
	utils.LogVerbose(">>> 发送未编码数据 - 开始 (0x%X)", receiver.Id)
	packet := packets.Packet{
		Mask: 0,
		Encrypted: false,
		Compressed: false,
		CompressSupport: false,
	}
	packet.ProtocolType = uint16(receiver.protocol >> 16)
	packet.ProtocolVer = uint16(receiver.protocol << 16 >> 16)
	packager := packets.PacketPackagerNBOrigin{}

	var buff bytes.Buffer
	for _, msg := range msgs {
		err, data := codecs.EncoderIMv1{}.Encode(&msg)
		if err == nil {
			buff.Write(data)
		}
	}

	err, data := packager.Package(&packet, buff.Bytes())
	if err != nil {
		return err
	}
	_, err = receiver.Handle.Write(data)
	if err != nil {
		return err
	}

	utils.LogVerbose("<<< 发送未编码数据 - 结束 (0x%X)", receiver.Id)
	return nil
}

func (receiver Connection) SendPacket(packet packets.Packet) (error) {
	utils.LogVerbose(">>> 发送消息封包 - 开始 (0x%X)", receiver.Id)
	packager := packets.PacketPackagerNBOrigin{}
	err, data := packager.Package(&packet, packet.Raw)
	if err != nil {
		return err
	}
	_, err = receiver.Handle.Write(data)
	if err != nil {
		return err
	}
	utils.LogVerbose("<<< 发送消息封包 - 结束 (0x%X)", receiver.Id)
	return nil
}