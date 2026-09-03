class QualifiedAnon$Inner {
  int v;
  final QualifiedAnon this$0;

  QualifiedAnon$Inner(QualifiedAnon arg0, int arg1) {
    this.this$0 = arg0;
    this.v = arg1;
  }

  int get() {
    return this.v + this.this$0.x;
  }
}
