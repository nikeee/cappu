abstract class VarargsAndAbstract {

  abstract int f(int arg0);

  int g(int... arg0) {
    return 0;
  }

  static double h(double arg0, double arg1) {
    return arg0;
  }
}
