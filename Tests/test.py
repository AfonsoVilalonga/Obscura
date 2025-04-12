from scapy.all import *
from netfilterqueue import NetfilterQueue  # Handles packets from iptables
from random import random

FRAME_DROP_RATE = 0.5  # Drop rate for RTP frames
packet_info = {}

def should_drop_frame():
    """Determine whether a frame should be dropped."""
    return random() < FRAME_DROP_RATE

def process_packet(packet):
    """Process packets intercepted by NFQUEUE."""
    scapy_packet = IP(packet.get_payload())  # Convert to Scapy packet
    if scapy_packet.haslayer(UDP):
        ip_layer = scapy_packet
        udp_layer = scapy_packet[UDP]
    
        target_ip = ip_layer.dst
        target_port = ip_layer.dport
        payload = bytes(udp_layer.payload)

        if len(payload) > 0 and payload[0] == 0x16:  # Check for DTLS-like packet
            target_hex = "576562525443"  # Hexadecimal target pattern
            other_target_hex = "776562727463"
            payload_hex = payload.hex()
            target_bytes = bytes.fromhex(target_hex)
            other_target = bytes.fromhex(other_target_hex)

            if target_bytes in bytes.fromhex(payload_hex) or other_target in bytes.fromhex(payload_hex):
                key = target_ip + str(target_port)
                if key in packet_info:
                    print(f"Packet already exists in dictionary.")
                else:
                    packet_info[key] = {
                        "target_ip": target_ip,
                        "target_port": target_port,
                    }
        else:
            key = target_ip + str(target_port)
            if key in packet_info or scapy_packet.haslayer(UDP):
                marker_bit = (payload[1] & 0x80) >> 7
                payload_type = payload[1] & 0x7F
                
                if payload_type > 77 and marker_bit == 1:
                    drop_next_frame = should_drop_frame()

                    if drop_next_frame:
                        print(f"Dropping packet: {scapy_packet.summary()}")
                        packet.drop()  # Drop the packet
                        return

    packet.accept()  # Allow other packets to pass

def main():
    """Main function to bind NFQUEUE and start packet processing."""
    nfqueue = NetfilterQueue()
    try:
        nfqueue.bind(0, process_packet)  # Bind to queue number 1
        print("Running packet handler...")
        nfqueue.run()
    except KeyboardInterrupt:
        print("Stopping packet handling...")
    finally:
        nfqueue.unbind()

if __name__ == "__main__":
    print("Setting up packet interception...")
    main()
