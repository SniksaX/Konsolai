# Konsolai bash integration. Source from ~/.bashrc.
[[ $- == *i* ]] || return

_konsolai_rewrite() {
  [[ $READLINE_LINE == @* ]] || return
  READLINE_LINE="konsolai -- $(printf '%q' "${READLINE_LINE#@}")"
  READLINE_POINT=${#READLINE_LINE}
}

bind -x '"\C-x\C-o": _konsolai_rewrite'
bind '"\C-m": "\C-x\C-o\C-j"'
