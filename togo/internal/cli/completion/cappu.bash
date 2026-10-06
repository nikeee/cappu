# bash completion for cappu
# Load it with: source <(cappu completion bash)

_cappu_files() {
  # $1: a compgen -X filter (e.g. '!*.java'), or empty for any file
  local f
  COMPREPLY=()
  while IFS= read -r f; do
    COMPREPLY+=("$f")
  done < <(compgen -f -X "$1" -- "$cur"; [[ -n $1 ]] && compgen -d -- "$cur")
  compopt -o filenames 2>/dev/null
}

_cappu() {
  local cur=${COMP_WORDS[COMP_CWORD]} prev=${COMP_WORDS[COMP_CWORD-1]}
  local cmd= args=0 i w

  for ((i = 1; i < COMP_CWORD; i++)); do
    w=${COMP_WORDS[i]}
    case $w in
      -c|--config|-o|--output|-p|--port|--format|--repo|--artifact) ((i++)) ;;
      -*) ;;
      *) if [[ -z $cmd ]]; then cmd=$w; else ((args++)); fi ;;
    esac
  done

  case $prev in
    -c|--config) _cappu_files ''; return ;;
    -o|--output) COMPREPLY=($(compgen -W "classes jar fat-jar" -- "$cur")); return ;;
    --format) COMPREPLY=($(compgen -W "text sarif" -- "$cur")); return ;;
    -p|--port|--repo|--artifact) return ;;
  esac

  if [[ -z $cmd ]]; then
    if [[ $cur == -* ]]; then
      COMPREPLY=($(compgen -W "-c --config -h --help --version" -- "$cur"))
    else
      COMPREPLY=($(compgen -W "init config-schema install update outdated tree add remove audit licenses verify search show publish compile check decompile format run test lsp dap mcp version self-upgrade rage cache completion" -- "$cur"))
    fi
    return
  fi

  local flags= first= files=
  case $cmd in
    init) flags="-y --yes --with-schema" ;;
    install) flags="-v --verbose --locked" ;;
    tree|licenses|search|show) flags="--json" ;;
    audit) flags="--no-cache --format" ;;
    publish) flags="--repo" ;;
    compile) flags="-o --output --artifact -q --quiet"; files='!*.java' ;;
    check) files='!*.java' ;;
    format) flags="-w --write"; files='!*.java' ;;
    decompile) flags="--disasm"; files='!*.class' ;;
    lsp|dap) flags="-p --port" ;;
    rage) flags="--open" ;;
    add|remove) first="api implementation annotationProcessor testImplementation" ;;
    version) first="major minor patch" ;;
    cache) first="clean verify" ;;
    completion) first="bash zsh" ;;
  esac

  if [[ $cur == -* ]]; then
    COMPREPLY=($(compgen -W "$flags -c --config -h --help" -- "$cur"))
  elif [[ -n $first ]]; then
    ((args == 0)) && COMPREPLY=($(compgen -W "$first" -- "$cur"))
  elif [[ -n $files ]]; then
    _cappu_files "$files"
  fi
}

complete -F _cappu cappu
