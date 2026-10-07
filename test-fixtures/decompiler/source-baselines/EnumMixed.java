enum EnumMixed {
  PLUS("+") {

    public int apply(int arg0, int arg1) {
      return arg0 + arg1;
    }
  },
  TIMES("*") {

    public int apply(int arg0, int arg1) {
      return arg0 * arg1;
    }
  },
  IDENT("=");
  private final java.lang.String sym;

  private EnumMixed(java.lang.String arg2) {
    this.sym = arg2;
  }

  public int apply(int arg0, int arg1) {
    return arg0;
  }

  public java.lang.String sym() {
    return this.sym;
  }

  public static void main(java.lang.String[] arg0) {
    EnumMixed var3;
    EnumMixed[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.println(var3.name() + var3.sym() + var3.apply(6, 7));
      var2++;
    }
  }
}
