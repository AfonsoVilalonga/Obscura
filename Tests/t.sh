#!/bin/bash

 # while true; do
  #    curl --socks5 127.0.0.1:10005 -w "Time to first byte: %{time_starttransfer} s\nTime until transfer began: %{time_pretransfer} s\nTotal time: %{time_total} s\nDownload speed: %{speed_download} bytes/sec\n" -o /dev/null  http://192.168.30.2:8080/download
  #    sleep 5
 # done




#   while true; do
#       curl --socks5 127.0.0.1:10005 -w "Time to first byte: %{time_starttransfer} s\nTime until transfer began: %{time_pretransfer} s\nTotal time: %{time_total} s\nDownload speed: %{speed_download} bytes/sec\n" -o /dev/null http://ipv4.download.thinkbroadband.com/5MB.zip
#       sleep 5
# done



  while true; do
      curl --socks5 127.0.0.1:10005 -w "Time to first byte: %{time_starttransfer} s\nTime until transfer began: %{time_pretransfer} s\nTotal time: %{time_total} s\nDownload speed: %{speed_download} bytes/sec\n" -o /dev/null  http://ipv4.download.thinkbroadband.com/5MB.zip
      sleep 5
  done

