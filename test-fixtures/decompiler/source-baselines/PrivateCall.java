class PrivateCall {

  private int secret(int arg0) {
    return arg0 * 2;
  }

  int use(int arg0) {
    return this.secret(arg0) + 1;
  }
}
