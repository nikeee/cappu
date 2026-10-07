enum EnumAbstract {
  LOW {

    public int rank() {
      return 1;
    }
  },
  HIGH {

    public int rank() {
      return 9;
    }
  };

  private EnumAbstract() {}

  public abstract int rank();

  public static void main(java.lang.String[] arg0) {
    EnumAbstract var3;
    EnumAbstract[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.println(var3.name() + var3.rank());
      var2++;
    }
  }
}
