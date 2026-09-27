"""Audit-only child-process network guard; never deployed with Workshop Agent."""
import socket
_connect=socket.socket.connect
_connect_ex=socket.socket.connect_ex
def check(address):
    if isinstance(address,tuple) and address[0] not in ('127.0.0.1','::1','localhost'):
        raise RuntimeError('Audit forbids non-loopback network')
def connect(self,address):
    check(address); return _connect(self,address)
def connect_ex(self,address):
    check(address); return _connect_ex(self,address)
socket.socket.connect=connect
socket.socket.connect_ex=connect_ex
