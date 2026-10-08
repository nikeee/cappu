public class QualifiedNew {
  int x;

  public QualifiedNew() {
    this.x = 7;
  }

  static int make(QualifiedNew arg0) {
    return arg0.new Inner(5).v;
  }

  class Inner {
    int v;

    Inner(int arg1) {
      this.v = arg1;
    }

    int sum() {
      return this.v + QualifiedNew.this.x;
    }
  }
}
