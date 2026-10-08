public class QualifiedNew {
  int x;

  public QualifiedNew() {
    this.x = 7;
  }

  static int make(QualifiedNew arg0) {
    return arg0.new Inner(5).v;
  }
}
