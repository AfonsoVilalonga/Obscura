'use strict';

const HEADER_SIZE              = 11;
const MAX_SIZE_DATA_IN_FRAME   = 1174;
const HASCONTENT               = 1;
const NOCONTENT                = 0;

let packetToEncode = [];
let remaining = null;
let sequenceNumber = 0;

onmessage = function (event) {
    const message = event.data

    if(message.command == "init"){
        const transformStream = new TransformStream({
            transform: encondeAdd,
        });
        message.r.pipeThrough(transformStream).pipeTo(message.w);
    }else if(message.command == "data"){
        blobToUint8Array(message.data).then(uint8Array => {
            packetToEncode.push(uint8Array)
        }).catch(error => {
            console.error('Error converting Blob to Uint8Array:', error);
        });
    }
}

function blobToUint8Array(blob) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();

        reader.onloadend = function() {
            if (reader.error) {
                reject(reader.error);
            } else {
                const arrayBuffer = reader.result;
                const uint8Array = new Uint8Array(arrayBuffer);
                resolve(uint8Array);
            }
        };
        reader.readAsArrayBuffer(blob);
    });
}

function uint32ToBytesBE(number) {
    const byteArray = [];

    byteArray[0] = (number >> 24) & 0xFF;
    byteArray[1] = (number >> 16) & 0xFF;
    byteArray[2] = (number >> 8) & 0xFF;
    byteArray[3] = number & 0xFF;

    return byteArray;
}

function encondeAdd(encodedFrame, controller) {
    if (encodedFrame instanceof RTCEncodedVideoFrame) {
        const numberOfPackets = Math.ceil(encodedFrame.data.byteLength / MAX_SIZE_DATA_IN_FRAME);    
        const basePacketSize = Math.floor(encodedFrame.data.byteLength / numberOfPackets);
        let remainingBytes = encodedFrame.data.byteLength % numberOfPackets;

        if(encodedFrame.data.byteLength <= HEADER_SIZE || packetToEncode.length == 0){
            const frameData = new DataView(encodedFrame.data)
            
            if(encodedFrame.data.byteLength <= MAX_SIZE_DATA_IN_FRAME){
                frameData.setInt8(0, NOCONTENT)
            }else{
                let inc = 0
                for(let i = 0; i < numberOfPackets; i += 1){
                    if(i + remainingBytes - 1 == numberOfPackets && remainingBytes > 1){
                        inc += 1;
                        remainingBytes -= 1
                    } 

                    frameData.setInt8(inc, NOCONTENT)
                    inc += basePacketSize
                }
            }   
         
            encodedFrame.data = frameData.buffer
            controller.enqueue(encodedFrame)
        }else{
            let data = new Uint8Array();
            let result = new Uint8Array();
            let chunck = 0;
            let rtpMaxPacketSize = basePacketSize
            let numberOfPacketsDone = 0
            
            while(numberOfPacketsDone < numberOfPackets){
                if(data.length == 0){
                    if(remaining == null){
                        data = packetToEncode.shift()
                        if(data == null){
                            data = new Uint8Array();
                            break
                        }
                    }else{
                        chunck = remaining.c
                        data = remaining.d
                        remaining = null
                    }
                }

                let header = []
                header = header.concat([HASCONTENT])
    
                //SEQUENCE NUMBER
                let sn = uint32ToBytesBE(sequenceNumber)
                header = header.concat(sn)
    
                //CHUNCK OF THE PACKET
                header = header.concat([chunck])
                chunck = chunck + 1
    
                //LEN 
                let len = Math.min(data.length, Math.max(rtpMaxPacketSize - HEADER_SIZE, 0))
                let lenArray = uint32ToBytesBE(len)
                header = header.concat(lenArray)
    
                //FINAL CHUNCK
                let finalChunck = 0;
                if(len == data.length){
                    finalChunck = 1;
                } 
                header = header.concat([finalChunck])
                
                //DATA
                let dataToappend = data.slice(0, len)
                let tmp = new Uint8Array(result.length + HEADER_SIZE + dataToappend.length);
                tmp.set(result, 0)
                tmp.set(header, result.length);
                tmp.set(dataToappend, HEADER_SIZE + result.length);
                result = tmp

                //UPDATES
                data = data.slice(len)
                rtpMaxPacketSize = rtpMaxPacketSize - len - HEADER_SIZE

                if (rtpMaxPacketSize <= HEADER_SIZE){
                    let filler = new Uint8Array(rtpMaxPacketSize)
                    let tmp = new Uint8Array(result.length + filler.length);
                    tmp.set(result, 0);
                    tmp.set(filler, result.length);
                    
                    result = tmp
    
                    rtpMaxPacketSize = basePacketSize
                    numberOfPacketsDone += 1
    
                    if(numberOfPacketsDone + remainingBytes == numberOfPackets && remainingBytes > 0){
                        rtpMaxPacketSize += 1
                        remainingBytes -= 1
                    } 
                }

                if (data.length == 0){
                    sequenceNumber = sequenceNumber + 1
                    chunck = 0
                    data = new Uint8Array()
                }
            }
       

            if(result.length < encodedFrame.data.byteLength){
                let filler = new Uint8Array(encodedFrame.data.byteLength - result.length)
                let tmp = new Uint8Array(result.length + filler.length);
                tmp.set(result, 0);
                tmp.set(filler, result.length);
    
                result = tmp
            }

            if(data.length > 0){
                let pair = {c: chunck, d: data}
                remaining = pair
            }
            encodedFrame.data = result.buffer;
            controller.enqueue(encodedFrame)
        }
    }else{
        controller.enqueue(encodedFrame)
    }
   
}