# INVITE and LISTEN placeholders are filled from this recording's actual key and free port.
Source style.tape
Output friend.mp4
Output friend.ascii
Hide
Type@1ms `export PS1='$ '; trap '' HUP; clear`
Enter
Sleep 500ms
Show
Type `infercat connect `
Type@1ms `@INVITE@`
Type ` --listen 127.0.0.1:@LISTEN@ &`
Enter
Hide
Type@1ms `echo $! > friend.pid`
Enter
Show
Wait+Screen@60s /path.*(direct|relayed)/
Sleep 2s
Type `curl -N 127.0.0.1:@LISTEN@/v1/chat/completions --json @question.json | tee response.txt`
Enter
Wait+Screen@60s /data:/
Sleep 4s
Ctrl+C
Sleep 2s
