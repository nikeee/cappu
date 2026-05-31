import java.time.LocalDate;
import java.util.List;

// Types outside the minimal JDK stub must still hover as the written syntax,
// never as "<error>".
class External {
  LocalDate /*today*/today;
  List<LocalDate> /*dates*/dates;
  Unknown /*u*/u;
  java.nio.file.Path /*path*/path;

  void take(LocalDate /*whenDecl*/when) {
    use(/*whenUse*/when);
  }
}
