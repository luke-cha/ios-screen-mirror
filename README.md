# ios-screen-mirror
This project is about the tool to transfer the screen of ios device in jpeg format to the designated place in tcp method.

 - For source development, the following github contents were referenced.
   - https://github.com/danielpaulus/quicktime_video_hack
   - https://github.com/nanoscopic/ios_video_pull
   
### Environment
The test was performed on macos, and it was not confirmed whether it could be performed on other os.
 - go version : 1.14
 - setup
 ```
 brew install libusb
 brew install pkg-config
 brew install gstreamer gst-plugins-bad gst-plugins-good gst-plugins-base gst-plugins-ugly
 ```

### Build
```
go build
```

### Run
0. prepare ios device and connect to your mac

1. clone ios-video-pull(https://github.com/nanoscopic/ios_video_pull), build and run
```
git clone https://github.com/nanoscopic/ios_video_pull.git
cd ios_video_pull
go get
go build
./ios-video-pull -stream
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
