# ios-screen-mirror
This project is about the tool to transfer the screen of ios device in jpeg format to the designated place in tcp method.

 - For source development, the following github contents were referenced.
   - https://github.com/danielpaulus/quicktime_video_hack
   - https://github.com/nanoscopic/ios_video_pull
   
### Environment
The test was performed on macos, and it was not confirmed whether it could be performed on other os.
 - go version : 1.14 (Recommended for version 1.12 or higher)
 - setup
 ```
 brew install libusb
 brew install pkg-config
 brew install ffmpeg
 brew install gstreamer gst-plugins-bad gst-plugins-good gst-plugins-base gst-plugins-ugly
 ```

### Build
```
go build
```

### Run
0. prepare ios device and connect to your mac

1. clone ios-video-stream(https://github.com/nanoscopic/ios_video_stream.git), build and run
```
git clone https://github.com/nanoscopic/ios_video_stream.git
cd ios_video_stream
go get
go build
./ios-video-stream -stream
```

2. clone this project, build and run
```
git clone https://github.com/jjunghyup/ios-screen-mirror.git
cd ios-screen-mirror
go get
go build
./ios-screen-mirror -pull
```

3. go to `http://localhost:8000` on your browser and click `open` button

### Usage
```
Usage of ./ios-screen-mirror:
  -devices
    	List devices then exit
  -file string
    	File to save h264 nalus into
  -pull
    	Pull video
  -pushSpec string
    	push image to tcp address (default "tcp://127.0.0.1:7879")
  -screenRatio float
    	Screen reduction ratio (default 0.5)
  -udid string
    	Device UDID
  -v	Verbose Debugging
```

### ETC
[in detail](https://velog.io/@chacha/아이폰-미러링-툴-소개)
