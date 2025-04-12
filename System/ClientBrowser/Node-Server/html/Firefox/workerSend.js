'use strict';

const HEADER_SIZE          = 11;
const bypassBytes          = 10;
let fragmentedPackets = [];
let keyFrame;

onrtctransform = (event) => {
    const transform = new TransformStream({
        start() {}, // Called on startup.
        flush() {}, 
        transform: decode,
    });

    event.transformer.readable.pipeThrough(transform).pipeTo(event.transformer.writable);
}

onmessage = (event) => {
    const message = event.data
    if(message.command == "chunk"){
        if (message.c.type == "key"){
            keyFrame = message.c
        }
    }
}

async function decode(encodedFrame, controller){
    if(encodedFrame instanceof RTCEncodedVideoFrame){        
        const dataRecv = new DataView(encodedFrame.data)
        let lenData = 0
        let sn = 0
        let ch = 0
        let fc = 0
        let data;
        for (let i = bypassBytes; i < dataRecv.byteLength && dataRecv.getUint8(i) == 1; i += (lenData + HEADER_SIZE)) {
            sn = dataRecv.getUint32(i+1)
            ch = dataRecv.getUint8(i+5)
            lenData = dataRecv.getUint32(i+6)
            fc = dataRecv.getUint8(i+10)

            data = encodedFrame.data.slice(i + HEADER_SIZE, lenData+HEADER_SIZE+i);

            let result = reconstructPacket(sn, ch, fc, data)
 
            if(result != null){
                self.postMessage({command: 'data', data: result}, [result]);
            }
        }

        let aux = keyFrame;
        if(aux != undefined){
            let ba = new ArrayBuffer(aux.byteLength)
            aux.copyTo(ba)
            encodedFrame.data = ba
            controller.enqueue(encodedFrame)
        }
    
        
        
    }else{
        controller.enqueue(encodedFrame)
    } 
}

function reconstructPacket(sn, ch, fc, data){
    if(ch == 0 && fc == 1){
        return data
    }

    let packet = fragmentedPackets[sn]
    let result = null
    if (packet == null){
        let aux = []
        aux[ch] = data
        
        let fp = {d: aux, lastChunck: 0}
        fragmentedPackets[sn] = fp

        let maxValueSequenceNumber = Math.pow(2, 32) - 1; 
        let getSymmetricPosition = maxValueSequenceNumber - sn;

        delete fragmentedPackets[getSymmetricPosition];

        return null
    }else{
        let chunckCurrent = packet.d[ch]
        if (chunckCurrent == null){
            packet.d[ch] = data

            if(fc == 1){
                packet.lastChunck = ch
            }

            if(packet.lastChunck != 0){
                result = new Uint8Array()
                for(let i = 0; i <= packet.lastChunck; i++){
                    let chunck_aux = packet.d[i]
                    if(chunck_aux != null){
                        let aux = new Uint8Array(chunck_aux)
                        let tmp = new Uint8Array(result.length + aux.length);
                        
                        
                        tmp.set(result, 0);
                        tmp.set(aux, result.length);
                        
                        result = tmp
                    }else{
                        result = null
                        break
                    }
                }
            }
      
        }
    }

    if(result != null){
        delete fragmentedPackets[sn]
        return result.buffer
    }
    
    return null
}