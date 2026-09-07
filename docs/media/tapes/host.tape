# make demo runs this inside a fresh temporary directory with the branch binary on PATH.
Source style.tape
# The real QR is 33 rows tall; keep it and the repeated link inside the frame.
Set FontSize 14
Output host.mp4
Output host.ascii
Hide
Type@1ms `export PS1='$ '; trap '' HUP; clear`
Enter
Sleep 500ms
Show
Type `infercat --data-dir host serve --upstream http://127.0.0.1:18080 --name "Max's laptop" &`
Enter
Hide
Type@1ms `echo $! > host.pid`
Enter
Show
Wait+Screen@60s /access.*friends reach/
Sleep 2s
Ctrl+L
Type `infercat --data-dir host keys add alice`
Enter
Wait+Screen /Send alice this link/
Sleep 5s
