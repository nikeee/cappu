public class ICast {

  static int use(java.lang.Object arg0) {
    ICast.A var1 = (ICast.A) (ICast.B) arg0;
    return var1.a();
  }

  interface A {

    public abstract int a();
  }

  interface B {

    public abstract int b();
  }
}
