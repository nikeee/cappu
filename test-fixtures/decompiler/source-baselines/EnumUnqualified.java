enum EnumUnqualified {
  A,
  B,
  C;

  private EnumUnqualified() {}

  public static void main(java.lang.String[] arg0) {
    EnumUnqualified var3;
    java.lang.System.out.println(values().length);
    java.lang.System.out.println(valueOf("B").ordinal());
    EnumUnqualified[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.print(var3.name());
      var2++;
    }
    java.lang.System.out.println();
  }

  static {
    A = new EnumUnqualified("A", 0);
    B = new EnumUnqualified("B", 1);
    C = new EnumUnqualified("C", 2);
    $VALUES = new EnumUnqualified[] {A, B, C};
  }
}
